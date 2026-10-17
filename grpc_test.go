package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func TestGRPCReflectionUnaryAndStream(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	gs := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(gs, hs)
	reflection.Register(gs)
	go gs.Serve(ln)
	defer gs.Stop()

	a := testApp(t)
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	q := "?t=" + a.token

	body, _ := json.Marshal(GRPCTarget{Target: ln.Addr().String()})
	resp, err := http.Post(srv.URL+"/api/grpc/describe"+q, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var svcs []GRPCService
	json.NewDecoder(resp.Body).Decode(&svcs)
	if len(svcs) != 1 || svcs[0].Name != "grpc.health.v1.Health" || len(svcs[0].Methods) < 2 {
		t.Fatalf("describe: %+v", svcs)
	}

	call := func(method string) []grpcMsg {
		ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1)+"/api/grpc/stream"+q, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		ws.WriteJSON(grpcMsg{Type: "connect", Method: method, GRPCTarget: GRPCTarget{Target: ln.Addr().String()}})
		ws.WriteJSON(grpcMsg{Type: "send", Data: `{"service": ""}`})
		var got []grpcMsg
		ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			var m grpcMsg
			if ws.ReadJSON(&m) != nil {
				return got
			}
			got = append(got, m)
			if m.Type == "end" || m.Type == "error" {
				return got
			}
			if m.Type == "message" && strings.HasSuffix(method, "Watch") {
				ws.WriteJSON(grpcMsg{Type: "cancel"})
			}
		}
	}
	unary := call("grpc.health.v1.Health/Check")
	last := unary[len(unary)-1]
	if last.Type != "end" || last.Code != "OK" || !strings.Contains(joinData(unary), "SERVING") {
		t.Errorf("unary: %+v", unary)
	}
	stream := call("grpc.health.v1.Health/Watch")
	if !strings.Contains(joinData(stream), "SERVING") {
		t.Errorf("stream: %+v", stream)
	}
}

func joinData(ms []grpcMsg) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.Type + ":" + m.Data + m.Message + "\n")
	}
	return b.String()
}
