package main

// Network capture: records CDP Network domain events to JSONL while the user
// drives the browser via the normal `rodney` verbs. Daemon child process
// holds the CDP subscription so events keep flowing between short-lived CLI
// invocations. Mirrors the existing `_proxy` helper pattern.
//
// Layout under stateDir()/net/:
//   current.session          # plain text, name of the active session dir
//   <session-id>/
//     pid                    # daemon's PID
//     meta.json              # session config (debug URL, body filter, max bytes)
//     daemon.log             # stdout/stderr of the daemon
//     requests.jsonl         # one event per line
//     bodies/<reqid>         # captured response body

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ---------- types & constants ----------

const (
	netDirName            = "net"
	currentSessionPointer = "current.session"
	sessionPIDFile        = "pid"
	sessionMetaFile       = "meta.json"
	sessionJSONLFile      = "requests.jsonl"
	sessionBodiesDir      = "bodies"

	defaultMaxBodyBytes int64 = 10 * 1024 * 1024 // 10 MiB per body
	stopWaitTimeout           = 5 * time.Second
	bodyFetchConcurrency      = 8 // parallel body fetches; bounded so we don't outrun Chrome's in-memory response cache
)

// Default resource types whose response bodies we eagerly capture.
var defaultBodyTypes = []string{"XHR", "Fetch", "WebSocket", "EventSource"}

// netEventType discriminates JSONL records. Used in both emit sites and the
// summariser switch.
type netEventType string

const (
	evtRequest  netEventType = "request"
	evtResponse netEventType = "response"
	evtLoaded   netEventType = "loaded"
	evtFailed   netEventType = "failed"
)

// bodyTypeNone is the sentinel passed to --body-types to disable body capture.
const bodyTypeNone = "none"

type netSessionMeta struct {
	SessionID    string    `json:"session_id"`
	Started      time.Time `json:"started"`
	DebugURL     string    `json:"debug_url"`
	BodyTypes    []string  `json:"body_types"`
	MaxBodyBytes int64     `json:"max_body_bytes"`
}

// One JSONL line per event. JSON tags stay short for compact on-disk lines;
// Go field names are full for readability.
type netEvent struct {
	Event           netEventType         `json:"event"`
	TS              time.Time            `json:"ts"`
	ReqID           string               `json:"reqid"`
	SID             string               `json:"sid,omitempty"`
	URL             string               `json:"url,omitempty"`
	Method          string               `json:"method,omitempty"`
	Type            string               `json:"type,omitempty"`
	Initiator       string               `json:"initiator,omitempty"`
	RequestHeaders  proto.NetworkHeaders `json:"req_headers,omitempty"`
	PostData        string               `json:"post_data,omitempty"`
	Status          int                  `json:"status,omitempty"`
	StatusText      string               `json:"status_text,omitempty"`
	MIME            string               `json:"mime,omitempty"`
	ResponseHeaders proto.NetworkHeaders `json:"res_headers,omitempty"`
	RemoteIP        string               `json:"remote_ip,omitempty"`
	RemotePort      int                  `json:"remote_port,omitempty"`
	EncodedLength   int64                `json:"encoded_len,omitempty"`
	BodyPath        string               `json:"body_path,omitempty"`
	BodySize        int64                `json:"body_size,omitempty"`
	BodyTruncated   bool                 `json:"body_truncated,omitempty"`
	Error           string               `json:"error,omitempty"`
	Canceled        bool                 `json:"canceled,omitempty"`
}

// ---------- path helpers ----------

func netDir() string                    { return filepath.Join(stateDir(), netDirName) }
func currentSessionPath() string        { return filepath.Join(netDir(), currentSessionPointer) }
func sessionPath(name string) string    { return filepath.Join(netDir(), name) }
func sessionMetaPath(p string) string   { return filepath.Join(p, sessionMetaFile) }
func sessionPIDPath(p string) string    { return filepath.Join(p, sessionPIDFile) }
func sessionJSONLPath(p string) string  { return filepath.Join(p, sessionJSONLFile) }
func sessionBodyDir(p string) string    { return filepath.Join(p, sessionBodiesDir) }

func readCurrentSession() (string, string, error) {
	data, err := os.ReadFile(currentSessionPath())
	if err != nil {
		return "", "", fmt.Errorf("no active network recording (run 'rodney network record start' first)")
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", "", fmt.Errorf("current.session is empty")
	}
	return name, sessionPath(name), nil
}

func loadSessionMeta(dir string) (*netSessionMeta, error) {
	data, err := os.ReadFile(sessionMetaPath(dir))
	if err != nil {
		return nil, err
	}
	var m netSessionMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------- top-level dispatcher ----------

func cmdNetwork(args []string) {
	if len(args) == 0 {
		fatal("network requires a subcommand: record | list | get | status")
	}
	switch args[0] {
	case "record":
		cmdNetworkRecord(args[1:])
	case "list":
		cmdNetworkList(args[1:])
	case "get":
		cmdNetworkGet(args[1:])
	case "status":
		cmdNetworkStatus(args[1:])
	default:
		fatal("unknown network subcommand: %s (want: record, list, get, status)", args[0])
	}
}

func cmdNetworkRecord(args []string) {
	if len(args) == 0 {
		fatal("network record requires: start | stop")
	}
	switch args[0] {
	case "start":
		cmdNetworkRecordStart(args[1:])
	case "stop":
		cmdNetworkRecordStop(args[1:])
	default:
		fatal("unknown network record subcommand: %s (want: start, stop)", args[0])
	}
}

// ---------- record start ----------

func cmdNetworkRecordStart(args []string) {
	fs := flag.NewFlagSet("network record start", flag.ContinueOnError)
	bodyTypes := fs.String("body-types", strings.Join(defaultBodyTypes, ","),
		"Comma-separated resource types to capture response bodies for (e.g. XHR,Fetch,WebSocket). Use 'none' to skip body capture.")
	maxBodyBytes := fs.Int64("max-body-bytes", defaultMaxBodyBytes,
		"Truncate response bodies larger than this many bytes (default 10MB)")
	if err := fs.Parse(args); err != nil {
		fatal("%s", err)
	}

	state, err := loadState()
	if err != nil {
		fatal("%s", err)
	}

	if name, _, err := readCurrentSession(); err == nil {
		fatal("a recording is already active (session %s). Run 'rodney network record stop' first.", name)
	}

	if err := os.MkdirAll(netDir(), 0755); err != nil {
		fatal("failed to create %s: %v", netDir(), err)
	}

	sessionID := time.Now().UTC().Format("20060102T150405Z")
	sdir := sessionPath(sessionID)
	if err := os.MkdirAll(sessionBodyDir(sdir), 0755); err != nil {
		fatal("failed to create session dir: %v", err)
	}

	types := splitAndTrim(*bodyTypes)
	meta := &netSessionMeta{
		SessionID:    sessionID,
		Started:      time.Now().UTC(),
		DebugURL:     state.DebugURL,
		BodyTypes:    types,
		MaxBodyBytes: *maxBodyBytes,
	}
	buf, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fatal("failed to marshal session meta: %v", err)
	}
	if err := os.WriteFile(sessionMetaPath(sdir), buf, 0644); err != nil {
		fatal("failed to write session meta: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		fatal("failed to resolve executable path: %v", err)
	}
	cmd := exec.Command(exe, "_netrec", sdir)
	setSysProcAttr(cmd)
	// Errors land in a log file inside the session dir for post-mortem.
	logf, _ := os.Create(filepath.Join(sdir, "daemon.log"))
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		fatal("failed to start netrec daemon: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()

	if err := os.WriteFile(sessionPIDPath(sdir), []byte(strconv.Itoa(pid)), 0644); err != nil {
		fatal("failed to write pid file: %v", err)
	}
	if err := os.WriteFile(currentSessionPath(), []byte(sessionID), 0644); err != nil {
		fatal("failed to mark current session: %v", err)
	}

	fmt.Printf("Recording started (session %s, daemon PID %d)\n", sessionID, pid)
	fmt.Printf("Session dir: %s\n", sdir)
	if len(types) == 1 && strings.EqualFold(types[0], bodyTypeNone) {
		fmt.Println("Body capture: off")
	} else {
		fmt.Printf("Body capture: %s (max %d bytes)\n", strings.Join(types, ","), *maxBodyBytes)
	}
}

// ---------- record stop ----------

func cmdNetworkRecordStop(_ []string) {
	name, sdir, err := readCurrentSession()
	if err != nil {
		fatal("%s", err)
	}

	pidBytes, err := os.ReadFile(sessionPIDPath(sdir))
	if err != nil {
		_ = os.Remove(currentSessionPath())
		fmt.Printf("No active daemon for session %s; cleaned up pointer.\n", name)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		fatal("corrupt pid file at %s: %v", sessionPIDPath(sdir), err)
	}

	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}

	deadline := time.Now().Add(stopWaitTimeout)
	alive := true
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			alive = false
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if alive {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	_ = os.Remove(sessionPIDPath(sdir))
	_ = os.Remove(currentSessionPath())

	events := countJSONL(sessionJSONLPath(sdir))
	dur := ""
	if meta, err := loadSessionMeta(sdir); err == nil {
		dur = time.Since(meta.Started).Round(time.Second).String()
	}
	fmt.Printf("Recording stopped (session %s)\n", name)
	fmt.Printf("Duration: %s, events: %d\n", dur, events)
	fmt.Printf("Session dir: %s\n", sdir)
}

// ---------- status ----------

func cmdNetworkStatus(_ []string) {
	name, sdir, err := readCurrentSession()
	if err != nil {
		fmt.Println("No active network recording.")
		return
	}
	meta, _ := loadSessionMeta(sdir)
	events := countJSONL(sessionJSONLPath(sdir))
	fmt.Printf("Active session: %s\n", name)
	if meta != nil {
		fmt.Printf("Started: %s (%s ago)\n", meta.Started.Format(time.RFC3339), time.Since(meta.Started).Round(time.Second))
		fmt.Printf("Body capture: %s (max %d bytes)\n", strings.Join(meta.BodyTypes, ","), meta.MaxBodyBytes)
	}
	fmt.Printf("Events: %d\n", events)
	fmt.Printf("Session dir: %s\n", sdir)
}

// ---------- list ----------

func cmdNetworkList(args []string) {
	fs := flag.NewFlagSet("network list", flag.ContinueOnError)
	typeFilter := fs.String("type", "", "Comma-separated resource types to include (e.g. XHR,Fetch). Default: all.")
	statusFilter := fs.String("status", "", "Filter by status. Single value (200) or range (200-299) or comma list.")
	urlContains := fs.String("url-contains", "", "Filter to URLs containing this substring.")
	limit := fs.Int("limit", 0, "Cap output at N rows (0 = no cap).")
	since := fs.Int("since", 0, "Only events newer than N seconds ago (0 = no cap).")
	format := fs.String("format", "table", "Output format: table | json | tsv")
	session := fs.String("session", "", "Session ID (default: current active session, or most recent if none active).")
	if err := fs.Parse(args); err != nil {
		fatal("%s", err)
	}

	sdir, _, err := resolveSessionDir(*session)
	if err != nil {
		fatal("%s", err)
	}

	events, err := readSessionEvents(sdir)
	if err != nil {
		fatal("%s", err)
	}

	rows := summarizeEvents(events)
	rows = filterRows(rows, *typeFilter, *statusFilter, *urlContains, *since)
	sort.Slice(rows, func(i, j int) bool { return rows[i].TS.Before(rows[j].TS) })
	if *limit > 0 && len(rows) > *limit {
		rows = rows[len(rows)-*limit:]
	}

	switch *format {
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(rows)
	case "tsv":
		fmt.Println("reqid\tmethod\tstatus\ttype\tms\tbytes\turl")
		for _, r := range rows {
			fmt.Printf("%s\t%s\t%d\t%s\t%d\t%d\t%s\n", r.ReqID, r.Method, r.Status, r.Type, r.DurationMS, r.BodySize, r.URL)
		}
	default:
		printRowsTable(rows)
	}
}

// ---------- get ----------

func cmdNetworkGet(args []string) {
	fs := flag.NewFlagSet("network get", flag.ContinueOnError)
	bodyOut := fs.String("body", "", "Write captured response body to this path. If '-' writes to stdout.")
	session := fs.String("session", "", "Session ID (default: current or most recent).")
	// Two-phase parse: Go's flag package stops at the first positional, so we
	// parse once to consume any leading flags, take the reqid, then parse the
	// remainder for trailing flags. Lets users write either order:
	//   rodney network get <reqid> --body PATH
	//   rodney network get --body PATH <reqid>
	if err := fs.Parse(args); err != nil {
		fatal("%s", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fatal("network get requires a request id (see 'rodney network list')")
	}
	reqid := rest[0]
	if len(rest) > 1 {
		if err := fs.Parse(rest[1:]); err != nil {
			fatal("%s", err)
		}
	}

	sdir, _, err := resolveSessionDir(*session)
	if err != nil {
		fatal("%s", err)
	}

	events, err := readSessionEvents(sdir)
	if err != nil {
		fatal("%s", err)
	}

	matched := make([]netEvent, 0, 4)
	for _, e := range events {
		if e.ReqID == reqid {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		fatal("no events for reqid %s in session %s", reqid, filepath.Base(sdir))
	}

	if *bodyOut != "" {
		var bodyPath string
		for _, e := range matched {
			if e.BodyPath != "" {
				bodyPath = e.BodyPath
				break
			}
		}
		if bodyPath == "" {
			fatal("no body captured for reqid %s (resource type may have been excluded from --body-types)", reqid)
		}
		if !filepath.IsAbs(bodyPath) {
			bodyPath = filepath.Join(sdir, bodyPath)
		}
		body, err := os.ReadFile(bodyPath)
		if err != nil {
			fatal("failed to read body at %s: %v", bodyPath, err)
		}
		if *bodyOut == "-" {
			_, _ = os.Stdout.Write(body)
		} else {
			if err := os.WriteFile(*bodyOut, body, 0644); err != nil {
				fatal("failed to write body to %s: %v", *bodyOut, err)
			}
			fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(body), *bodyOut)
		}
		return
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(matched)
}

// ---------- _netrec daemon (internal) ----------

// cmdNetrecDaemon takes only the session directory; all other config is read
// from meta.json. Reading from meta lets the daemon survive a parent process
// that exited mid-detach and keeps the wire interface minimal.
func cmdNetrecDaemon(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "_netrec requires <session-dir>")
		os.Exit(1)
	}
	sdir := args[0]

	meta, err := loadSessionMeta(sdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: load meta from %s failed: %v\n", sdir, err)
		os.Exit(1)
	}

	browser := rod.New().ControlURL(meta.DebugURL)
	if err := browser.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: connect failed: %v\n", err)
		os.Exit(1)
	}

	bodyTypeSet := make(map[proto.NetworkResourceType]bool, len(meta.BodyTypes))
	captureBodies := true
	if len(meta.BodyTypes) == 1 && strings.EqualFold(meta.BodyTypes[0], bodyTypeNone) {
		captureBodies = false
	} else {
		for _, t := range meta.BodyTypes {
			bodyTypeSet[proto.NetworkResourceType(t)] = true
		}
	}

	// Pre-create the bodies dir so the hot path doesn't pay an mkdir per fetch.
	bodyDir := sessionBodyDir(sdir)
	if err := os.MkdirAll(bodyDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: mkdir bodies failed: %v\n", err)
		os.Exit(1)
	}

	jf, err := os.OpenFile(sessionJSONLPath(sdir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: open jsonl failed: %v\n", err)
		os.Exit(1)
	}
	defer jf.Close()

	var jmu sync.Mutex
	emit := func(e netEvent) {
		e.TS = time.Now().UTC()
		// Marshal outside the lock; only the file write is serialised.
		buf, err := json.Marshal(&e)
		if err != nil {
			return
		}
		buf = append(buf, '\n')
		jmu.Lock()
		_, _ = jf.Write(buf)
		jmu.Unlock()
	}

	type reqMeta struct {
		Type proto.NetworkResourceType
	}
	reqs := make(map[proto.NetworkRequestID]reqMeta)
	sessions := make(map[proto.TargetSessionID]*rod.Page)
	var mu sync.Mutex

	enableNetwork := func(p *rod.Page) {
		_ = proto.NetworkEnable{}.Call(p)
		mu.Lock()
		sessions[p.SessionID] = p
		mu.Unlock()
	}

	if pages, err := browser.Pages(); err == nil {
		for _, p := range pages {
			enableNetwork(p)
		}
	}

	// Bounded-concurrency body fetcher. The CDP event loop in EachEvent
	// dispatches handlers serially, so synchronous body fetches inside the
	// handler would block all subsequent events. Offloading to goroutines lets
	// the event stream keep flowing; the semaphore caps concurrency so we
	// don't outrun Chrome's in-memory response cache, and the WaitGroup lets
	// the daemon drain in-flight fetches on shutdown.
	bodySem := make(chan struct{}, bodyFetchConcurrency)
	var bodyWG sync.WaitGroup
	maxBodyBytes := meta.MaxBodyBytes

	go browser.EachEvent(
		func(e *proto.TargetTargetCreated) {
			page, err := browser.PageFromTarget(e.TargetInfo.TargetID)
			if err != nil {
				return
			}
			enableNetwork(page)
		},
		func(e *proto.TargetDetachedFromTarget) {
			mu.Lock()
			delete(sessions, e.SessionID)
			mu.Unlock()
		},
		func(e *proto.NetworkRequestWillBeSent, sid proto.TargetSessionID) {
			mu.Lock()
			reqs[e.RequestID] = reqMeta{Type: e.Type}
			mu.Unlock()
			ev := netEvent{
				Event:          evtRequest,
				ReqID:          string(e.RequestID),
				SID:            string(sid),
				URL:            e.Request.URL,
				Method:         e.Request.Method,
				Type:           string(e.Type),
				RequestHeaders: e.Request.Headers,
				PostData:       e.Request.PostData,
			}
			if e.Initiator != nil {
				ev.Initiator = string(e.Initiator.Type)
			}
			emit(ev)
		},
		func(e *proto.NetworkResponseReceived, sid proto.TargetSessionID) {
			mu.Lock()
			reqs[e.RequestID] = reqMeta{Type: e.Type}
			mu.Unlock()
			ev := netEvent{
				Event:           evtResponse,
				ReqID:           string(e.RequestID),
				SID:             string(sid),
				URL:             e.Response.URL,
				Type:            string(e.Type),
				Status:          e.Response.Status,
				StatusText:      e.Response.StatusText,
				MIME:            e.Response.MIMEType,
				ResponseHeaders: e.Response.Headers,
				RemoteIP:        e.Response.RemoteIPAddress,
			}
			if e.Response.RemotePort != nil {
				ev.RemotePort = *e.Response.RemotePort
			}
			emit(ev)
		},
		func(e *proto.NetworkLoadingFinished, sid proto.TargetSessionID) {
			reqID := e.RequestID
			encLen := int64(e.EncodedDataLength)
			sidStr := string(sid)

			mu.Lock()
			info, hasMeta := reqs[reqID]
			delete(reqs, reqID)
			page := sessions[sid]
			mu.Unlock()

			shouldCapture := captureBodies && hasMeta && bodyTypeSet[info.Type] && page != nil
			if !shouldCapture {
				emit(netEvent{
					Event:         evtLoaded,
					ReqID:         string(reqID),
					SID:           sidStr,
					EncodedLength: encLen,
				})
				return
			}

			bodyWG.Add(1)
			go func() {
				defer bodyWG.Done()
				bodySem <- struct{}{}
				defer func() { <-bodySem }()

				ev := netEvent{
					Event:         evtLoaded,
					ReqID:         string(reqID),
					SID:           sidStr,
					EncodedLength: encLen,
				}
				if res, err := (proto.NetworkGetResponseBody{RequestID: reqID}).Call(page); err == nil {
					writeBodyFile(bodyDir, string(reqID), res, maxBodyBytes, &ev)
				}
				emit(ev)
			}()
		},
		func(e *proto.NetworkLoadingFailed, sid proto.TargetSessionID) {
			mu.Lock()
			delete(reqs, e.RequestID)
			mu.Unlock()
			emit(netEvent{
				Event:    evtFailed,
				ReqID:    string(e.RequestID),
				SID:      string(sid),
				Type:     string(e.Type),
				Error:    e.ErrorText,
				Canceled: e.Canceled,
			})
		},
	)()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	bodyWG.Wait()
	_ = jf.Sync()
}

// writeBodyFile persists the captured response body and stamps the result onto
// ev. Truncates to maxBytes. base64-encoded bodies are decoded first. Errors
// are swallowed — the loaded event still gets emitted, just without body
// fields, so callers can see which requests had bodies skipped.
func writeBodyFile(bodyDir, reqID string, res *proto.NetworkGetResponseBodyResult, maxBytes int64, ev *netEvent) {
	var data []byte
	var trunc bool
	if res.Base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(res.Body)
		if err != nil {
			return
		}
		if int64(len(decoded)) > maxBytes {
			decoded = decoded[:maxBytes]
			trunc = true
		}
		data = decoded
	} else {
		// Slice the string before the []byte copy to avoid an oversized alloc.
		s := res.Body
		if int64(len(s)) > maxBytes {
			s = s[:maxBytes]
			trunc = true
		}
		data = []byte(s)
	}
	path := filepath.Join(bodyDir, reqID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return
	}
	rel := filepath.Join(sessionBodiesDir, reqID)
	ev.BodyPath = rel
	ev.BodySize = int64(len(data))
	ev.BodyTruncated = trunc
}

// ---------- row summarisation for list/get ----------

type netRow struct {
	ReqID      string    `json:"reqid"`
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Type       string    `json:"type"`
	Status     int       `json:"status"`
	BodySize   int64     `json:"body_size"`
	DurationMS int64     `json:"duration_ms"`
	TS         time.Time `json:"ts"`
	HasBody    bool      `json:"has_body"`
	Error      string    `json:"error,omitempty"`
}

func summarizeEvents(events []netEvent) []netRow {
	byID := make(map[string]*netRow, len(events)/3+1)
	requestTS := make(map[string]time.Time)
	for _, e := range events {
		r, ok := byID[e.ReqID]
		if !ok {
			r = &netRow{ReqID: e.ReqID, TS: e.TS}
			byID[e.ReqID] = r
		}
		switch e.Event {
		case evtRequest:
			requestTS[e.ReqID] = e.TS
			r.URL = e.URL
			r.Method = e.Method
			r.Type = e.Type
			if r.TS.IsZero() {
				r.TS = e.TS
			}
		case evtResponse:
			r.Status = e.Status
			if r.Type == "" {
				r.Type = e.Type
			}
		case evtLoaded:
			if e.BodyPath != "" {
				r.HasBody = true
				r.BodySize = e.BodySize
			}
			if start, ok := requestTS[e.ReqID]; ok {
				r.DurationMS = e.TS.Sub(start).Milliseconds()
			}
		case evtFailed:
			r.Error = e.Error
		}
	}
	out := make([]netRow, 0, len(byID))
	for _, r := range byID {
		out = append(out, *r)
	}
	return out
}

func filterRows(rows []netRow, typeFilter, statusFilter, urlContains string, sinceSec int) []netRow {
	types := splitAndTrim(typeFilter)
	statusOK := makeStatusMatcher(statusFilter)
	out := make([]netRow, 0, len(rows))
	cutoff := time.Time{}
	if sinceSec > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(sinceSec) * time.Second)
	}
	for _, r := range rows {
		if len(types) > 0 && !containsCI(types, r.Type) {
			continue
		}
		if !statusOK(r.Status) {
			continue
		}
		if urlContains != "" && !strings.Contains(r.URL, urlContains) {
			continue
		}
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func printRowsTable(rows []netRow) {
	if len(rows) == 0 {
		fmt.Println("(no requests matched)")
		return
	}
	fmt.Printf("%-13s %-6s %-6s %-12s %-7s %-9s %s\n", "REQID", "METHOD", "STATUS", "TYPE", "MS", "BYTES", "URL")
	for _, r := range rows {
		status := ""
		if r.Status > 0 {
			status = strconv.Itoa(r.Status)
		} else if r.Error != "" {
			status = "ERR"
		}
		url := r.URL
		if len(url) > 90 {
			url = url[:87] + "..."
		}
		fmt.Printf("%-13s %-6s %-6s %-12s %-7d %-9d %s\n",
			r.ReqID, r.Method, status, r.Type, r.DurationMS, r.BodySize, url)
	}
}

// ---------- helpers ----------

func resolveSessionDir(name string) (string, string, error) {
	if name != "" {
		p := sessionPath(name)
		if _, err := os.Stat(p); err != nil {
			return "", "", fmt.Errorf("session %s not found at %s", name, p)
		}
		return p, name, nil
	}
	if cur, p, err := readCurrentSession(); err == nil {
		return p, cur, nil
	}
	entries, err := os.ReadDir(netDir())
	if err != nil {
		return "", "", fmt.Errorf("no recordings found at %s", netDir())
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		return "", "", fmt.Errorf("no recordings found at %s", netDir())
	}
	sort.Strings(dirs)
	last := dirs[len(dirs)-1]
	return sessionPath(last), last, nil
}

func readSessionEvents(sdir string) ([]netEvent, error) {
	f, err := os.Open(sessionJSONLPath(sdir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	out := make([]netEvent, 0, 256)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e netEvent
		if err := json.Unmarshal(line, &e); err == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// countJSONL counts newline-terminated records via a single ReadFile +
// bytes.Count. Faster than running a bufio.Scanner just to tally lines.
func countJSONL(p string) int {
	data, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	return bytes.Count(data, []byte{'\n'})
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsCI(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

// makeStatusMatcher returns a predicate for the --status filter spec. Empty
// spec matches all. Non-empty spec that fails to parse anything matches none
// (no silent accept-everything for typos like "abc").
func makeStatusMatcher(spec string) func(int) bool {
	if spec == "" {
		return func(int) bool { return true }
	}
	ranges := make([][2]int, 0)
	exact := make(map[int]bool)
	for _, part := range splitAndTrim(spec) {
		if strings.Contains(part, "-") {
			pieces := strings.SplitN(part, "-", 2)
			lo, lerr := strconv.Atoi(strings.TrimSpace(pieces[0]))
			hi, herr := strconv.Atoi(strings.TrimSpace(pieces[1]))
			if lerr == nil && herr == nil && lo <= hi {
				ranges = append(ranges, [2]int{lo, hi})
			}
		} else if n, err := strconv.Atoi(part); err == nil {
			exact[n] = true
		}
	}
	return func(status int) bool {
		if status == 0 {
			return false
		}
		if exact[status] {
			return true
		}
		for _, r := range ranges {
			if status >= r[0] && status <= r[1] {
				return true
			}
		}
		return false
	}
}
