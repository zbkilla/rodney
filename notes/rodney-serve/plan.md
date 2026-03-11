# Plan: Implement `rodney serve` command

## Summary

Add a `rodney serve` command to `main.go` that runs a long-lived process communicating over newline-delimited JSON on stdin/stdout. This enables the Python library to own Chrome's lifecycle via pipe-based crash safety, and eliminates per-command subprocess + WebSocket reconnection overhead.

## Protocol

Newline-delimited JSON over stdin (requests) and stdout (responses):

```
→ {"id": 1, "cmd": "open", "args": ["https://example.com"]}
← {"id": 1, "ok": true, "stdout": "Example Domain", "stderr": ""}

→ {"id": 2, "cmd": "click", "args": ["#missing"]}
← {"id": 2, "ok": false, "exit_code": 2, "stdout": "", "stderr": "error: element not found"}
```

## Implementation Steps

### Step 1: Define serve request/response types

Add JSON-serializable structs in `main.go`:

```go
type serveRequest struct {
    ID   int      `json:"id"`
    Cmd  string   `json:"cmd"`
    Args []string `json:"args,omitempty"`
}

type serveResponse struct {
    ID       int    `json:"id"`
    OK       bool   `json:"ok"`
    Stdout   string `json:"stdout,omitempty"`
    Stderr   string `json:"stderr,omitempty"`
    ExitCode int    `json:"exit_code,omitempty"`
}
```

### Step 2: Implement `cmdServe`

The core function that:

1. **Launches Chrome** — reuses the same launcher logic from `cmdStart`, but with `Leakless(true)` (or manual PID tracking) so Chrome dies when rodney dies.
2. **Connects and keeps the connection open** — one `rod.Browser` + active page, persisted across commands (no reconnect per command).
3. **Reads stdin line-by-line** in a loop, dispatching each JSON request to the appropriate command handler.
4. **Watches for stdin EOF** — when detected, kills Chrome, cleans up, and exits. This is the crash-safety mechanism.
5. **Writes JSON responses to stdout** — one line per response, matching the request `id`.

Key differences from the current CLI pattern:
- No `state.json` needed — all state lives in-memory (browser connection, active page index, data dir).
- No `withPage()` calls — the browser/page are held in a struct passed to each handler.
- Commands write to a captured buffer instead of directly to `os.Stdout`/`os.Stderr`.
- `fatal()` / `os.Exit()` must never be called — errors become JSON error responses.

### Step 3: Create a serve-mode session struct

```go
type serveSession struct {
    browser    *rod.Browser
    state      *State       // in-memory state (active page, etc.)
    timeout    time.Duration
    stateDir   string
}
```

This holds the persistent browser connection and mutable state (active page index) that currently lives in `state.json`.

### Step 4: Refactor command handlers for serve mode

The existing `cmdXxx` functions call `withPage()` (which loads state from disk + connects) and use `fatal()` for errors and `fmt.Print*` for output. For serve mode, we need to capture stdout/stderr and return errors instead of exiting.

**Approach**: Create new `serveXxx` wrapper functions (or a single `dispatchServe` function) that:
- Accept the `serveSession` and args
- Capture stdout/stderr by redirecting to `bytes.Buffer`
- Recover from panics (rod uses `Must*` patterns that panic)
- Return `(stdout string, stderr string, exitCode int)`

For the dispatch, we have a big switch on `req.Cmd` that maps to the same command logic but using the in-memory session instead of disk state. The cleanest approach is a `dispatchServe` function that:

1. Sets up stdout/stderr capture
2. Switches on the command name
3. Calls the existing command logic (most commands just call `withPage()` then do one thing on the page)
4. Returns captured output

Since the existing commands are tightly coupled to `withPage()` + `fatal()` + direct stdout, the pragmatic approach is to **re-implement the command dispatch in serve mode** using the session's already-connected browser/page. Each command is 3-10 lines of rod calls — the total new code is manageable and avoids fragile refactoring of 40+ existing functions.

### Step 5: Stdin EOF monitoring

Run a goroutine that watches for stdin EOF:

```go
go func() {
    io.Copy(io.Discard, os.Stdin) // blocks until EOF
    // Parent died — clean up
    browser.MustClose()
    os.Exit(0)
}()
```

Actually, since the main loop reads stdin line-by-line, EOF is naturally detected when `scanner.Scan()` returns false. So:

- Main loop: `bufio.Scanner` on stdin, `scanner.Scan()` returns false on EOF
- On EOF: kill Chrome, clean up state dir, exit

No separate goroutine needed for the simple single-session model.

### Step 6: Add "serve" to the command dispatcher

In `main()`, add:
```go
case "serve":
    cmdServe(args)
```

### Step 7: Handle `start` and `stop` within serve mode

- `start` is implicit — Chrome launches when `rodney serve` starts. The first response could be a "ready" message, or the protocol just starts accepting commands immediately.
- `stop` in serve mode = close stdin (from Python side) or send a `{"cmd": "stop"}` request which triggers graceful shutdown.

### Step 8: Flags for `rodney serve`

Same flags as `rodney start`:
- `--show` (non-headless)
- `--insecure` / `-k` (ignore cert errors)

Parsed at the start of `cmdServe`, before entering the main loop.

### Step 9: Tests

Add tests in `main_test.go` that:
- Launch `rodney serve` as a subprocess
- Send JSON requests via stdin pipe
- Read JSON responses from stdout pipe
- Verify correct responses for basic commands (open, title, html, click, etc.)
- Verify that closing stdin causes the process to exit (and Chrome to die)

## File Changes

- **`main.go`**: Add `serveRequest`/`serveResponse` types, `serveSession` struct, `cmdServe` function with main loop + command dispatch, add `"serve"` case to `main()` switch.
- **`help.txt`**: Add `serve` to the help text.
- **`main_test.go`**: Add serve mode tests.

## What's NOT in scope

- Multi-session / browser contexts (future enhancement per the design doc recommendation)
- Python library changes (separate repo/effort)
- Any changes to existing CLI command behavior
