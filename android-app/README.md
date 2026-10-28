# sxops Remote for Android

A native Jetpack Compose companion for a running sxops host. This is a remote controller, not an Android port of the Go server stack. Existing Fyne desktop and CLI behavior remains available.

## Build the debug APK

With JDK 17, Android SDK platform 35 and Gradle 8.10.2 installed:

```sh
gradle :app:assembleDebug
```

APK output: `app/build/outputs/apk/debug/app-debug.apk`. GitHub Actions builds and uploads this APK on pushes/PRs affecting the Android app or API. Debug APKs are not Play Store release-signed.

## Enable the host API

Use a trusted TLS certificate whose hostname matches the URL entered on Android. Never use plain HTTP or disable certificate validation.

```sh
export SXOPS_REMOTE_TOKEN="$(openssl rand -hex 32)"
./sxops-linux remote --listen 0.0.0.0:8787 --cert /path/fullchain.pem --key /path/privkey.pem
```

Use your actual executable name. Keep the token secret; do not commit it or pass it as a command-line argument. Restrict port 8787 at the host firewall to trusted networks/devices. A public listener is not safe merely because it uses a token. The API exposes only `GET /api/v1/status`, `POST /api/v1/start`, and `POST /api/v1/stop`; stop leaves Tor running. Every request requires `Authorization: Bearer <token>`. TLS 1.2+ and a token of at least 32 characters are mandatory. The Android client uses normal platform certificate validation. Self-signed certificates are not trusted by default; use a certificate trusted by Android or install your private CA through managed device trust settings.

The initial version does not expose setup, arbitrary command execution, credentials, file paths, logs, moderation, or update installation. The token is memory-only in this prototype. Local Android hosting is a separate future porting effort.
