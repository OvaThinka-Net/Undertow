# Undertow Admin Panel — Audit (2026-04-27)

## Priority 1 — Security / Correctness

### 1. Token expiry not enforced
**File:** `cmd/admin/main.go` lines 876-883
`validateToken()` only verifies HMAC signature, never checks the timestamp. Tokens are valid forever until server restart. `SessionHours`/`maxAge` only affects cookie TTL in the browser — a copied token works indefinitely.
**Fix:** Parse timestamp from token, reject if `time.Now() - ts > maxAge`.

### 2. Password printed in plaintext to stdout/journalctl
**File:** `cmd/admin/main.go` line 1339
```go
log.Printf("║  Pass: %-30s║", cfg.Password)
```
Visible in `journalctl -u undertow`. Anyone with shell access sees the admin password.
**Fix:** Mask it (e.g. `****`) or omit entirely.

### 3. No HTTP server timeouts
**File:** `cmd/admin/main.go` line 1345
```go
srv := &http.Server{Addr: listenAddr, Handler: handler}
```
No `ReadTimeout`, `WriteTimeout`, `ReadHeaderTimeout`, `MaxHeaderBytes`. Vulnerable to slow-loris connection exhaustion.
**Fix:** Add `ReadHeaderTimeout: 10s`, `ReadTimeout: 30s`, `WriteTimeout: 60s`, `MaxHeaderBytes: 1<<20`.

### 4. Folder name injection in Drive API query
**File:** `cmd/admin/main.go` line 667
```go
q := fmt.Sprintf("name = '%s' and mimeType = ...", folderName)
```
User-supplied `folderName` is interpolated without escaping single quotes. A name containing `'` would break or manipulate the query.
**Fix:** Escape single quotes: `strings.ReplaceAll(folderName, "'", "\\'")`.

---

## Priority 2 — Logic / UX

### 5. No auto-start of server process
Admin starts, prints banner, but does NOT auto-start the server child process. After every reboot/restart, someone must manually click "Start" in the UI.
**Fix:** If config + credentials + token + server binary all exist, auto-start on boot. Add `auto_start: true` config option.

### 6. Client platform suggestion uses server's GOARCH
**File:** `cmd/admin/main.go` line 1102
```go
if runtime.GOARCH == "arm64" {
    suggested = "darwin-arm64"
```
This checks the **server's** architecture (linux-amd64), not the client's browser. Always suggests Intel Mac when server is x86.
**Fix:** Default to `darwin-arm64` for macOS (Apple Silicon is dominant now), or remove the GOARCH check.

### 7. Deprecated OAuth token endpoint
**File:** `cmd/admin/main.go` line 636
```go
https://www.googleapis.com/oauth2/v4/token
```
While `handleOAuthExchange` uses the correct `https://oauth2.googleapis.com/token`. Inconsistent.
**Fix:** Change to `https://oauth2.googleapis.com/token` everywhere.

### 8. Restart race condition
**File:** `cmd/admin/main.go` line 375
```go
pm.Stop()
time.Sleep(500 * time.Millisecond)
pm.Start()
```
500ms gap where another concurrent request could call Start() first.
**Fix:** Hold the mutex across stop+start, or use a restart-specific lock.

---

## Priority 3 — Hardening

### 9. No CSRF protection
All POST endpoints accept any request with a valid session cookie. A malicious page could submit forms to the admin panel.
**Fix:** Check `Origin`/`Referer` header, or add `X-Requested-With` requirement on API calls.

### 10. Missing security headers
No `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`.
**Fix:** Add middleware with `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, basic CSP.

### 11. Non-atomic config writes
**Files:** lines 421, 755, 993
`os.WriteFile` directly to config path. A crash mid-write corrupts the file.
**Fix:** Write to temp file, then `os.Rename`.

### 12. SSE stream has no heartbeat
**File:** `cmd/admin/main.go` lines 798-817
If no logs flow, the SSE connection may be silently dropped by reverse proxies (Traefik/nginx) with idle timeouts.
**Fix:** Send `:keepalive\n\n` comment every 15-30 seconds.

---

## Priority 4 — Polish

### 13. No version display
No build version shown in UI or API. Hard to confirm which build is deployed.
**Fix:** Inject version via `-ldflags` at build time, expose in `/api/status` and UI footer.

### 14. Log buffer is memory-only
All 2000 log entries lost on restart. No way to debug past crashes.
**Fix:** Optional log file output alongside the ring buffer.

### 15. Config round-trip loses field order/types
**File:** `cmd/admin/main.go` lines 406-420
Config POST unmarshals to `map[string]interface{}` then re-marshals. JSON integers become float64, field order changes, duplicate keys collapse.
**Fix:** Use `json.RawMessage` or preserve original bytes, only patching specific fields.

---

## Summary Table

| # | Issue | Severity | Effort |
|---|-------|----------|--------|
| 1 | Token expiry not enforced | **High** | Low |
| 2 | Password in logs | **High** | Trivial |
| 3 | No HTTP timeouts | **Medium** | Trivial |
| 4 | Folder name injection | **Medium** | Trivial |
| 5 | No auto-start | **Medium** | Low |
| 6 | Wrong GOARCH for suggestions | **Medium** | Trivial |
| 7 | Deprecated token endpoint | **Low** | Trivial |
| 8 | Restart race condition | **Low** | Low |
| 9 | No CSRF protection | **Medium** | Medium |
| 10 | Security headers | **Low** | Low |
| 11 | Non-atomic config writes | **Low** | Low |
| 12 | SSE no heartbeat | **Low** | Low |
| 13 | No version display | **Low** | Low |
| 14 | Log persistence | **Low** | Medium |
| 15 | Config round-trip loss | **Low** | Medium |
