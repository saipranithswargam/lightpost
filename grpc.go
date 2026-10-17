package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"
)

// gRPC support: services are discovered through server reflection or local
// .proto files, and every call type (unary, client/server streaming, bidi) is
// driven over one WebSocket bridge so the UI can stream in both directions.

type GRPCTarget struct {
	Target      string   `json:"target"`
	TLS         bool     `json:"tls"`
	ProtoFiles  []string `json:"protoFiles,omitempty"`
	ImportPaths []string `json:"importPaths,omitempty"`
	Metadata    []KV     `json:"metadata,omitempty"`
	EnvID       string   `json:"envId,omitempty"`
}

type GRPCMethod struct {
	Name            string `json:"name"`
	FullName        string `json:"fullName"` // pkg.Service/Method
	ClientStreaming bool   `json:"clientStreaming"`
	ServerStreaming bool   `json:"serverStreaming"`
	InputType       string `json:"inputType"`
	OutputType      string `json:"outputType"`
	Example         string `json:"example"`
}

type GRPCService struct {
	Name    string       `json:"name"`
	Methods []GRPCMethod `json:"methods"`
}

func (a *App) grpcVars(envID string) *Vars { return varsFor(a.store.Workspace(), nil, envID, nil) }

func (a *App) grpcDial(t GRPCTarget) (*grpc.ClientConn, error) {
	target := strings.TrimSpace(t.Target)
	target = strings.TrimPrefix(strings.TrimPrefix(target, "grpc://"), "grpcs://")
	if target == "" {
		return nil, errors.New("server address is required (host:port)")
	}
	var creds credentials.TransportCredentials
	if t.TLS {
		ws := a.store.Workspace()
		host := target
		if i := strings.LastIndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		tr, err := a.engine.transport(ws.Settings, ws.Settings.VerifySSL, host)
		if err != nil {
			return nil, err
		}
		tc := tr.TLSClientConfig.Clone()
		tc.NextProtos = nil
		creds = credentials.NewTLS(tc)
	} else {
		creds = insecure.NewCredentials()
	}
	return grpc.NewClient(target, grpc.WithTransportCredentials(creds))
}

func mdFrom(kvs []KV) metadata.MD {
	md := metadata.MD{}
	for _, kv := range kvs {
		if kv.Enabled && kv.Key != "" {
			md.Append(strings.ToLower(kv.Key), kv.Value)
		}
	}
	return md
}

// services resolves descriptors from .proto files or server reflection.
func (a *App) grpcServices(ctx context.Context, conn *grpc.ClientConn, t GRPCTarget) ([]*desc.ServiceDescriptor, error) {
	if len(t.ProtoFiles) > 0 {
		p := protoparse.Parser{ImportPaths: t.ImportPaths, IncludeSourceCodeInfo: false}
		if len(p.ImportPaths) == 0 {
			// Resolve each file relative to its own directory.
			seen := map[string]bool{}
			var names []string
			for _, f := range t.ProtoFiles {
				dir, base := splitDir(f)
				if !seen[dir] {
					p.ImportPaths = append(p.ImportPaths, dir)
					seen[dir] = true
				}
				names = append(names, base)
			}
			t.ProtoFiles = names
		}
		fds, err := p.ParseFiles(t.ProtoFiles...)
		if err != nil {
			return nil, fmt.Errorf("parse .proto: %w", err)
		}
		var out []*desc.ServiceDescriptor
		for _, fd := range fds {
			out = append(out, fd.GetServices()...)
		}
		return out, nil
	}
	rctx := metadata.NewOutgoingContext(ctx, mdFrom(t.Metadata))
	rc := grpcreflect.NewClientAuto(rctx, conn)
	defer rc.Reset()
	names, err := rc.ListServices()
	if err != nil {
		return nil, fmt.Errorf("server reflection unavailable (%v); import .proto files instead", err)
	}
	sort.Strings(names)
	var out []*desc.ServiceDescriptor
	for _, n := range names {
		if strings.HasPrefix(n, "grpc.reflection.") {
			continue
		}
		sd, err := rc.ResolveService(n)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", n, err)
		}
		out = append(out, sd)
	}
	return out, nil
}

func splitDir(p string) (string, string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ".", p
	}
	return p[:i], p[i+1:]
}

func (a *App) resolveGRPCTarget(t GRPCTarget) GRPCTarget {
	v := a.grpcVars(t.EnvID)
	t.Target = v.Resolve(t.Target)
	t.Metadata = resolveKVs(t.Metadata, v)
	return t
}

func (a *App) handleGRPCDescribe(w http.ResponseWriter, r *http.Request) {
	var t GRPCTarget
	if err := decode(r, &t); err != nil {
		writeErr(w, 400, err)
		return
	}
	t = a.resolveGRPCTarget(t)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var conn *grpc.ClientConn
	if len(t.ProtoFiles) == 0 {
		c, err := a.grpcDial(t)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		defer c.Close()
		conn = c
	}
	sds, err := a.grpcServices(ctx, conn, t)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	out := []GRPCService{}
	for _, sd := range sds {
		s := GRPCService{Name: sd.GetFullyQualifiedName()}
		for _, md := range sd.GetMethods() {
			ex, _ := dynamic.NewMessage(md.GetInputType()).MarshalJSONIndent()
			if tpl := exampleJSON(md.GetInputType(), 0); tpl != "" {
				ex = []byte(tpl)
			}
			s.Methods = append(s.Methods, GRPCMethod{Name: md.GetName(), FullName: sd.GetFullyQualifiedName() + "/" + md.GetName(),
				ClientStreaming: md.IsClientStreaming(), ServerStreaming: md.IsServerStreaming(),
				InputType: md.GetInputType().GetFullyQualifiedName(), OutputType: md.GetOutputType().GetFullyQualifiedName(), Example: string(ex)})
		}
		out = append(out, s)
	}
	writeJSON(w, out)
}

// exampleJSON renders a message template with every field set to a sample
// value so users can see the shape they need to send.
func exampleJSON(md *desc.MessageDescriptor, depth int) string {
	var b strings.Builder
	writeExample(&b, md, depth, "")
	return b.String()
}

func writeExample(b *strings.Builder, md *desc.MessageDescriptor, depth int, indent string) {
	fields := md.GetFields()
	if len(fields) == 0 || depth > 4 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	for i, f := range fields {
		b.WriteString(indent + "  " + jsonString(f.GetJSONName()) + ": ")
		if f.IsMap() {
			b.WriteString("{}")
		} else {
			if f.IsRepeated() {
				b.WriteString("[")
			}
			writeScalar(b, f, depth, indent+"  ")
			if f.IsRepeated() {
				b.WriteString("]")
			}
		}
		if i < len(fields)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(indent + "}")
}

func writeScalar(b *strings.Builder, f *desc.FieldDescriptor, depth int, indent string) {
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		switch f.GetMessageType().GetFullyQualifiedName() {
		case "google.protobuf.Timestamp":
			b.WriteString(`"2024-01-01T00:00:00Z"`)
		case "google.protobuf.Duration":
			b.WriteString(`"1s"`)
		case "google.protobuf.StringValue":
			b.WriteString(`""`)
		case "google.protobuf.Struct", "google.protobuf.Any":
			b.WriteString("{}")
		default:
			writeExample(b, f.GetMessageType(), depth+1, indent)
		}
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		b.WriteString(`""`)
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		b.WriteString(`""`)
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		b.WriteString("false")
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		vals := f.GetEnumType().GetValues()
		if len(vals) > 0 {
			b.WriteString(jsonString(vals[0].GetName()))
		} else {
			b.WriteString("0")
		}
	case descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_TYPE_UINT64, descriptorpb.FieldDescriptorProto_TYPE_SINT64, descriptorpb.FieldDescriptorProto_TYPE_FIXED64, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		b.WriteString(`"0"`)
	default:
		b.WriteString("0")
	}
}

type grpcMsg struct {
	GRPCTarget
	Type    string `json:"type"`
	Method  string `json:"method,omitempty"`
	Data    string `json:"data,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Headers []KV   `json:"headers,omitempty"`
	Time    int64  `json:"time,omitempty"`
	Elapsed int64  `json:"elapsedMs,omitempty"`
}

func mdKVs(md metadata.MD) []KV {
	var out []KV
	for k, vs := range md {
		for _, v := range vs {
			out = append(out, KV{Key: k, Value: v, Enabled: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (a *App) handleGRPCStream(w http.ResponseWriter, r *http.Request) {
	ui, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ui.Close()
	var wmu sync.Mutex
	send := func(m grpcMsg) {
		m.Time = time.Now().UnixMilli()
		wmu.Lock()
		defer wmu.Unlock()
		ui.WriteJSON(m)
	}
	var cfg grpcMsg
	if err := ui.ReadJSON(&cfg); err != nil || cfg.Type != "connect" {
		return
	}
	cfg.GRPCTarget = a.resolveGRPCTarget(cfg.GRPCTarget)
	vars := a.grpcVars(cfg.EnvID)
	fail := func(err error) { send(grpcMsg{Type: "error", Message: err.Error()}) }

	conn, err := a.grpcDial(cfg.GRPCTarget)
	if err != nil {
		fail(err)
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sds, err := a.grpcServices(ctx, conn, cfg.GRPCTarget)
	if err != nil {
		fail(err)
		return
	}
	svc, meth, _ := strings.Cut(cfg.Method, "/")
	var md *desc.MethodDescriptor
	for _, sd := range sds {
		if sd.GetFullyQualifiedName() == svc {
			md = sd.FindMethodByName(meth)
		}
	}
	if md == nil {
		fail(fmt.Errorf("method %s not found", cfg.Method))
		return
	}
	ctx = metadata.NewOutgoingContext(ctx, mdFrom(cfg.Metadata))
	stub := grpcdynamic.NewStub(conn)
	start := time.Now()
	var header, trailer metadata.MD

	parse := func(js string) (*dynamic.Message, error) {
		m := dynamic.NewMessage(md.GetInputType())
		js = strings.TrimSpace(vars.Resolve(js))
		if js == "" {
			js = "{}"
		}
		if err := m.UnmarshalJSON([]byte(js)); err != nil {
			return nil, fmt.Errorf("invalid %s JSON: %w", md.GetInputType().GetName(), err)
		}
		return m, nil
	}
	emitMsg := func(pm any) {
		if dm, ok := pm.(*dynamic.Message); ok {
			b, err := dm.MarshalJSONIndent()
			if err != nil {
				fail(err)
				return
			}
			send(grpcMsg{Type: "message", Data: string(b)})
		}
	}
	finish := func(err error) {
		st := status.Convert(err)
		send(grpcMsg{Type: "end", Code: st.Code().String(), Message: st.Message(), Headers: mdKVs(trailer), Elapsed: time.Since(start).Milliseconds()})
	}
	send(grpcMsg{Type: "open", Method: cfg.Method, Data: fmt.Sprintf("%v/%v", md.IsClientStreaming(), md.IsServerStreaming())})

	// Messages from the UI arrive on this channel; closing means "end stream".
	in := make(chan string, 16)
	go func() {
		defer close(in)
		for {
			var m grpcMsg
			if ui.ReadJSON(&m) != nil {
				cancel()
				return
			}
			switch m.Type {
			case "send":
				in <- m.Data
			case "end":
				return
			case "cancel":
				cancel()
				return
			}
		}
	}()

	sendHeaders := func() {
		if len(header) > 0 {
			send(grpcMsg{Type: "headers", Headers: mdKVs(header)})
		}
	}
	switch {
	case !md.IsClientStreaming() && !md.IsServerStreaming():
		js, ok := <-in
		if !ok {
			return
		}
		req, err := parse(js)
		if err != nil {
			fail(err)
			return
		}
		send(grpcMsg{Type: "sent", Data: js})
		resp, err := stub.InvokeRpc(ctx, md, req, grpc.Header(&header), grpc.Trailer(&trailer))
		sendHeaders()
		if err == nil {
			emitMsg(resp)
		}
		finish(err)
	case !md.IsClientStreaming() && md.IsServerStreaming():
		js, ok := <-in
		if !ok {
			return
		}
		req, err := parse(js)
		if err != nil {
			fail(err)
			return
		}
		send(grpcMsg{Type: "sent", Data: js})
		ss, err := stub.InvokeRpcServerStream(ctx, md, req)
		if err != nil {
			finish(err)
			return
		}
		header, _ = ss.Header()
		sendHeaders()
		for {
			resp, err := ss.RecvMsg()
			if err != nil {
				trailer = ss.Trailer()
				if errors.Is(err, io.EOF) {
					err = nil
				}
				finish(err)
				return
			}
			emitMsg(resp)
		}
	case md.IsClientStreaming() && !md.IsServerStreaming():
		cs, err := stub.InvokeRpcClientStream(ctx, md)
		if err != nil {
			finish(err)
			return
		}
		for js := range in {
			req, err := parse(js)
			if err != nil {
				fail(err)
				continue
			}
			if err := cs.SendMsg(req); err != nil {
				break
			}
			send(grpcMsg{Type: "sent", Data: js})
		}
		resp, err := cs.CloseAndReceive()
		header, _ = cs.Header()
		trailer = cs.Trailer()
		sendHeaders()
		if err == nil {
			emitMsg(resp)
		}
		finish(err)
	default: // bidi
		bs, err := stub.InvokeRpcBidiStream(ctx, md)
		if err != nil {
			finish(err)
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if h, err := bs.Header(); err == nil {
				header = h
				sendHeaders()
			}
			for {
				resp, err := bs.RecvMsg()
				if err != nil {
					trailer = bs.Trailer()
					if errors.Is(err, io.EOF) {
						err = nil
					}
					finish(err)
					return
				}
				emitMsg(resp)
			}
		}()
		for js := range in {
			req, err := parse(js)
			if err != nil {
				fail(err)
				continue
			}
			if err := bs.SendMsg(req); err != nil {
				break
			}
			send(grpcMsg{Type: "sent", Data: js})
		}
		bs.CloseSend()
		<-done
	}
	// Give the UI a moment to read the final frame before closing.
	wmu.Lock()
	ui.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	wmu.Unlock()
}
