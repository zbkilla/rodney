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
//     meta.json              # {session_id, started, debug_url, body_types, max_body_bytes}
//     requests.jsonl         # one event per line
//     bodies/<reqid>.bin     # captured response body (binary or text)

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

	defaultMaxBodyBytes int64 = 10 * 1024 * 1024 // 10 MB per body
	stopWaitTimeout           = 5 * time.Second
)

// resource types that we eagerly capture response bodies for by default
var defaultBodyTypes = []string{"XHR", "Fetch", "WebSocket", "EventSource"}

type netSessionMeta struct {
	SessionID    string    `json:"session_id"`
	Started      time.Time `json:"started"`
	DebugURL     string    `json:"debug_url"`
	BodyTypes    []string  `json:"body_types"`
	MaxBodyBytes int64     `json:"max_body_bytes"`
}

// One JSONL line per event. Fields are omitempty so each event type only
// carries what's relevant.
type netEvent struct {
	Event     string                 `json:"event"` // request | response | loaded | failed
	TS        time.Time              `json:"ts"`
	ReqID     string                 `json:"reqid"`
	SID       string                 `json:"sid,omitempty"`
	URL       string                 `json:"url,omitempty"`
	Method    string                 `json:"method,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Initiator string                 `json:"initiator,omitempty"`
	ReqHdr    map[string]interface{} `json:"req_headers,omitempty"`
	PostData  string                 `json:"post_data,omitempty"`
	Status    int                    `json:"status,omitempty"`
	StatusTxt string                 `json:"status_text,omitempty"`
	MIME      string                 `json:"mime,omitempty"`
	ResHdr    map[string]interface{} `json:"res_headers,omitempty"`
	RemoteIP  string                 `json:"remote_ip,omitempty"`
	RemotePrt int                    `json:"remote_port,omitempty"`
	EncodedLn int64                  `json:"encoded_len,omitempty"`
	BodyPath  string                 `json:"body_path,omitempty"`
	BodySize  int64                  `json:"body_size,omitempty"`
	BodyTrunc bool                   `json:"body_truncated,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Canceled  bool                   `json:"canceled,omitempty"`
}

// ---------- path helpers ----------

func netDir() string                  { return filepath.Join(stateDir(), netDirName) }
func currentSessionPath() string      { return filepath.Join(netDir(), currentSessionPointer) }
func sessionPath(name string) string  { return filepath.Join(netDir(), name) }
func sessionMetaPath(p string) string { return filepath.Join(p, sessionMetaFile) }
func sessionPIDPath(p string) string  { return filepath.Join(p, sessionPIDFile) }
func sessionJSONLPath(p string) string {
	return filepath.Join(p, sessionJSONLFile)
}
func sessionBodyDir(p string) string { return filepath.Join(p, sessionBodiesDir) }

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

	// Refuse to start if one is already recording.
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
	if buf, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(sessionMetaPath(sdir), buf, 0644)
	}

	exe, err := os.Executable()
	if err != nil {
		fatal("failed to resolve executable path: %v", err)
	}
	childArgs := []string{"_netrec", sdir, state.DebugURL, strings.Join(types, ","), strconv.FormatInt(*maxBodyBytes, 10)}
	cmd := exec.Command(exe, childArgs...)
	setSysProcAttr(cmd)
	// Detach stdout/stderr so the child can fire and forget. Errors land in a
	// log file inside the session dir for post-mortem.
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
	if len(types) == 1 && strings.EqualFold(types[0], "none") {
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
		// no pid file — daemon already gone, just clean up
		_ = os.Remove(currentSessionPath())
		fmt.Printf("No active daemon for session %s; cleaned up pointer.\n", name)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		fatal("corrupt pid file at %s: %v", sessionPIDPath(sdir), err)
	}

	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}

	// Poll for exit
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
		// last resort
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

func cmdNetrecDaemon(args []string) {
	if len(args) < 2 {
		fatal("_netrec requires: <session-dir> <debug-url> [<body-types>] [<max-body-bytes>]")
	}
	sdir := args[0]
	debugURL := args[1]
	bodyTypes := splitAndTrim(getArg(args, 2, strings.Join(defaultBodyTypes, ",")))
	maxBodyBytes := defaultMaxBodyBytes
	if v := getArg(args, 3, ""); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			maxBodyBytes = parsed
		}
	}

	browser := rod.New().ControlURL(debugURL)
	if err := browser.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: connect failed: %v\n", err)
		os.Exit(1)
	}

	bodyTypeSet := make(map[proto.NetworkResourceType]bool, len(bodyTypes))
	captureBodies := true
	if len(bodyTypes) == 1 && strings.EqualFold(bodyTypes[0], "none") {
		captureBodies = false
	} else {
		for _, t := range bodyTypes {
			bodyTypeSet[proto.NetworkResourceType(t)] = true
		}
	}

	jsonlPath := sessionJSONLPath(sdir)
	jf, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "_netrec: open jsonl failed: %v\n", err)
		os.Exit(1)
	}
	defer jf.Close()
	jw := json.NewEncoder(jf)
	var jmu sync.Mutex

	emit := func(e netEvent) {
		e.TS = time.Now().UTC()
		jmu.Lock()
		defer jmu.Unlock()
		_ = jw.Encode(e)
	}

	// per-request bookkeeping (type lookup for body filtering, URL for failed events)
	type reqMeta struct {
		Type proto.NetworkResourceType
		URL  string
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

	go browser.EachEvent(
		func(e *proto.TargetTargetCreated) {
			page, err := browser.PageFromTarget(e.TargetInfo.TargetID)
			if err != nil {
				return
			}
			enableNetwork(page)
		},
		func(e *proto.NetworkRequestWillBeSent, sid proto.TargetSessionID) {
			mu.Lock()
			reqs[e.RequestID] = reqMeta{Type: e.Type, URL: e.Request.URL}
			mu.Unlock()
			ev := netEvent{
				Event:    "request",
				ReqID:    string(e.RequestID),
				SID:      string(sid),
				URL:      e.Request.URL,
				Method:   e.Request.Method,
				Type:     string(e.Type),
				ReqHdr:   headersToMap(e.Request.Headers),
				PostData: e.Request.PostData,
			}
			if e.Initiator != nil {
				ev.Initiator = string(e.Initiator.Type)
			}
			emit(ev)
		},
		func(e *proto.NetworkResponseReceived, sid proto.TargetSessionID) {
			// keep type fresh in case ResponseReceived has a different type than RequestWillBeSent
			mu.Lock()
			meta := reqs[e.RequestID]
			meta.Type = e.Type
			reqs[e.RequestID] = meta
			mu.Unlock()
			ev := netEvent{
				Event:     "response",
				ReqID:     string(e.RequestID),
				SID:       string(sid),
				URL:       e.Response.URL,
				Type:      string(e.Type),
				Status:    e.Response.Status,
				StatusTxt: e.Response.StatusText,
				MIME:      e.Response.MIMEType,
				ResHdr:    headersToMap(e.Response.Headers),
				RemoteIP:  e.Response.RemoteIPAddress,
			}
			if e.Response.RemotePort != nil {
				ev.RemotePrt = *e.Response.RemotePort
			}
			emit(ev)
		},
		func(e *proto.NetworkLoadingFinished, sid proto.TargetSessionID) {
			mu.Lock()
			meta, ok := reqs[e.RequestID]
			page := sessions[sid]
			mu.Unlock()

			ev := netEvent{
				Event:     "loaded",
				ReqID:     string(e.RequestID),
				SID:       string(sid),
				EncodedLn: int64(e.EncodedDataLength),
			}

			if captureBodies && ok && bodyTypeSet[meta.Type] && page != nil {
				res, err := proto.NetworkGetResponseBody{RequestID: e.RequestID}.Call(page)
				if err == nil {
					var raw []byte
					if res.Base64Encoded {
						raw, _ = base64.StdEncoding.DecodeString(res.Body)
					} else {
						raw = []byte(res.Body)
					}
					trunc := false
					if int64(len(raw)) > maxBodyBytes {
						raw = raw[:maxBodyBytes]
						trunc = true
					}
					bodyRel := filepath.Join(sessionBodiesDir, string(e.RequestID))
					bodyAbs := filepath.Join(sdir, bodyRel)
					_ = os.MkdirAll(filepath.Dir(bodyAbs), 0755)
					if werr := os.WriteFile(bodyAbs, raw, 0644); werr == nil {
						ev.BodyPath = bodyRel
						ev.BodySize = int64(len(raw))
						ev.BodyTrunc = trunc
					}
				}
			}
			emit(ev)
		},
		func(e *proto.NetworkLoadingFailed, sid proto.TargetSessionID) {
			emit(netEvent{
				Event:    "failed",
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
	_ = jf.Sync()
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
	byID := make(map[string]*netRow)
	requestTS := make(map[string]time.Time)
	for _, e := range events {
		r, ok := byID[e.ReqID]
		if !ok {
			r = &netRow{ReqID: e.ReqID, TS: e.TS}
			byID[e.ReqID] = r
		}
		switch e.Event {
		case "request":
			requestTS[e.ReqID] = e.TS
			r.URL = e.URL
			r.Method = e.Method
			r.Type = e.Type
			if r.TS.IsZero() {
				r.TS = e.TS
			}
		case "response":
			r.Status = e.Status
			if r.Type == "" {
				r.Type = e.Type
			}
		case "loaded":
			if e.BodyPath != "" {
				r.HasBody = true
				r.BodySize = e.BodySize
			}
			if start, ok := requestTS[e.ReqID]; ok {
				r.DurationMS = e.TS.Sub(start).Milliseconds()
			}
		case "failed":
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
	// fall back to most recent dir under netDir()
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

func countJSONL(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n
}

func headersToMap(h proto.NetworkHeaders) map[string]interface{} {
	if h == nil {
		return nil
	}
	out := make(map[string]interface{}, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
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
		return len(exact) == 0 && len(ranges) == 0
	}
}

func getArg(args []string, idx int, fallback string) string {
	if idx >= len(args) {
		return fallback
	}
	return args[idx]
}

var _ io.Reader = (*os.File)(nil) // pin io import; used for bufio.Scanner buffer sizing context
