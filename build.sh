#!/bin/sh
# Builds the lightpost binary and a double-clickable Lightpost.app bundle.
set -e
cd "$(dirname "$0")"
go build -trimpath -ldflags="-s -w" -o lightpost .
APP=Lightpost.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp lightpost "$APP/Contents/MacOS/Lightpost"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Lightpost</string>
  <key>CFBundleDisplayName</key><string>Lightpost</string>
  <key>CFBundleIdentifier</key><string>dev.lightpost.app</string>
  <key>CFBundleExecutable</key><string>Lightpost</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0.0</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
codesign --force --sign - "$APP" 2>/dev/null || true
echo "Built ./lightpost and ./$APP"
