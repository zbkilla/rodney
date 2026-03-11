package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

//go:embed help.txt
var helpText string

var version = "dev"

// scopeMode determines whether to use a local or global state directory.
type scopeMode int

const (
	scopeAuto   scopeMode = iota // auto-detect: local if .rodney/state.json exists in cwd, else global
	scopeLocal                   // force local (./.rodney/)
	scopeGlobal                  // force global (~/.rodney/)
)

// activeStateDir is set once at startup based on --local/--global flags.
var activeStateDir string

// extractScopeArgs scans args for --local/--global, removes them, and returns the mode.
// If both appear, the last one wins.
func extractScopeArgs(args []string) (scopeMode, []string) {
	mode := scopeAuto
	var filtered []string
	for _, arg := range args {
		switch arg {
		case "--local":
			mode = scopeLocal
		case "--global":
			mode = scopeGlobal
		default:
			filtered = append(filtered, arg)
		}
	}
	return mode, filtered
}

// resolveStateDir determines the state directory based on scope mode and working directory.
func resolveStateDir(mode scopeMode, workingDir string) string {
	switch mode {
	case scopeLocal:
		return filepath.Join(workingDir, ".rodney")
	case scopeGlobal:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".rodney")
	default: // scopeAuto
		localDir := filepath.Join(workingDir, ".rodney")
		if _, err := os.Stat(filepath.Join(localDir, "state.json")); err == nil {
			return localDir
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".rodney")
	}
}

// State persisted between CLI invocations
type State struct {
	DebugURL    string `json:"debug_url"`
	ChromePID   int    `json:"chrome_pid"`
	ActivePage  int    `json:"active_page"`  // index into pages list
	DataDir     string `json:"data_dir"`
	ProxyPID    int    `json:"proxy_pid,omitempty"`  // PID of auth proxy helper
	ProxyPort   int    `json:"proxy_port,omitempty"` // local port of auth proxy
}

func stateDir() string {
	if dir := os.Getenv("RODNEY_HOME"); dir != "" {
		return dir
	}
	if activeStateDir != "" {
		return activeStateDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".rodney")
}

func statePath() string {
	return filepath.Join(stateDir(), "state.json")
}

func loadState() (*State, error) {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return nil, fmt.Errorf("no browser session (run 'rodney start' first)")
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt state file: %w", err)
	}
	return &s, nil
}

func saveState(s *State) error {
	if err := os.MkdirAll(stateDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), data, 0644)
}

func removeState() {
	os.Remove(statePath())
}

// connectBrowser connects to the running Chrome instance
func connectBrowser(s *State) (*rod.Browser, error) {
	browser := rod.New().ControlURL(s.DebugURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser (is it still running?): %w", err)
	}
	return browser, nil
}

// getActivePage returns the currently active page
func getActivePage(browser *rod.Browser, s *State) (*rod.Page, error) {
	pages, err := browser.Pages()
	if err != nil {
		return nil, fmt.Errorf("failed to list pages: %w", err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages open")
	}
	idx := s.ActivePage
	if idx < 0 || idx >= len(pages) {
		idx = 0
	}
	return pages[idx], nil
}

func printUsage() {
	fmt.Print(helpText)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	// Extract --local/--global from all args before dispatching
	mode, cleanedArgs := extractScopeArgs(os.Args[1:])
	if len(cleanedArgs) == 0 {
		printUsage()
		os.Exit(1)
	}

	wd, _ := os.Getwd()
	activeStateDir = resolveStateDir(mode, wd)

	cmd := cleanedArgs[0]
	args := cleanedArgs[1:]

	if cmd == "--version" {
		fmt.Println(version)
		os.Exit(0)
	}

	switch cmd {
	case "_proxy":
		cmdInternalProxy(args) // hidden: runs the auth proxy helper
	case "start":
		cmdStart(args)
	case "connect":
		cmdConnect(args)
	case "stop":
		cmdStop(args)
	case "status":
		cmdStatus(args)
	case "open":
		cmdOpen(args)
	case "back":
		cmdBack(args)
	case "forward":
		cmdForward(args)
	case "reload":
		cmdReload(args)
	case "clear-cache":
		cmdClearCache(args)
	case "url":
		cmdURL(args)
	case "title":
		cmdTitle(args)
	case "html":
		cmdHTML(args)
	case "text":
		cmdText(args)
	case "attr":
		cmdAttr(args)
	case "pdf":
		cmdPDF(args)
	case "js":
		cmdJS(args)
	case "click":
		cmdClick(args)
	case "input":
		cmdInput(args)
	case "clear":
		cmdClear(args)
	case "select":
		cmdSelect(args)
	case "submit":
		cmdSubmit(args)
	case "hover":
		cmdHover(args)
	case "file":
		cmdFile(args)
	case "download":
		cmdDownload(args)
	case "focus":
		cmdFocus(args)
	case "wait":
		cmdWait(args)
	case "waitload":
		cmdWaitLoad(args)
	case "waitstable":
		cmdWaitStable(args)
	case "waitidle":
		cmdWaitIdle(args)
	case "sleep":
		cmdSleep(args)
	case "screenshot":
		cmdScreenshot(args)
	case "screenshot-el":
		cmdScreenshotEl(args)
	case "pages":
		cmdPages(args)
	case "page":
		cmdPage(args)
	case "newpage":
		cmdNewPage(args)
	case "closepage":
		cmdClosePage(args)
	case "exists":
		cmdExists(args)
	case "count":
		cmdCount(args)
	case "visible":
		cmdVisible(args)
	case "assert":
		cmdAssert(args)
	case "ax-tree":
		cmdAXTree(args)
	case "ax-find":
		cmdAXFind(args)
	case "ax-node":
		cmdAXNode(args)
	case "serve":
		cmdServe(args)
	case "help", "-h", "--help":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(2)
	}
}

// Default timeout for element queries (seconds)
var defaultTimeout = 30 * time.Second

func init() {
	if t := os.Getenv("ROD_TIMEOUT"); t != "" {
		if secs, err := strconv.ParseFloat(t, 64); err == nil {
			defaultTimeout = time.Duration(secs * float64(time.Second))
		}
	}
}

// withPage loads state, connects, and returns the active page.
// Caller should NOT close the browser (we just disconnect).
func withPage() (*State, *rod.Browser, *rod.Page) {
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	browser, err := connectBrowser(s)
	if err != nil {
		fatal("%v", err)
	}
	page, err := getActivePage(browser, s)
	if err != nil {
		fatal("%v", err)
	}
	// Apply default timeout so element queries don't hang forever
	page = page.Timeout(defaultTimeout)
	return s, browser, page
}

// Ignore SIGPIPE for piped output
func init() {
	signal.Ignore(syscall.SIGPIPE)
}

// launchChrome starts Chrome with the given options and saves state.
// If detach is true, Chrome survives after rodney exits (CLI mode).
// If detach is false, Chrome dies when rodney dies (serve mode).
// Messages are written to msgOut (pass io.Discard to suppress).
// Returns the State and connected rod.Browser.
func launchChrome(headless bool, ignoreCertErrors bool, detach bool, msgOut ...io.Writer) (*State, *rod.Browser) {
	var w io.Writer = os.Stdout
	if len(msgOut) > 0 {
		w = msgOut[0]
	}
	dataDir := filepath.Join(stateDir(), "chrome-data")
	os.MkdirAll(dataDir, 0755)

	l := launcher.New().
		Set("no-sandbox").
		Set("disable-gpu").
		Set("single-process"). // Required for screenshots in gVisor/container environments
		Leakless(!detach).     // Leakless(false) = Chrome survives; Leakless(true) = Chrome dies with parent
		UserDataDir(dataDir).
		Headless(headless)

	// When in non-headless mode, make sure that we show the startup window immediately
	// (instead of showing a window only after calling "rodney open")
	if !headless {
		l = l.Delete("no-startup-window")
	}

	if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
		l = l.Bin(bin)
	}

	// Detect authenticated proxy and launch helper if needed
	var proxyPID, proxyPort int
	if server, user, pass, needed := detectProxy(); needed {
		authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))

		// Find a free port for the local proxy
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatal("failed to find free port for proxy: %v", err)
		}
		proxyPort = ln.Addr().(*net.TCPAddr).Port
		ln.Close()

		// Launch ourselves as the proxy helper in the background
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "_proxy",
			strconv.Itoa(proxyPort), server, authHeader)
		setSysProcAttr(cmd)
		if err := cmd.Start(); err != nil {
			fatal("failed to start proxy helper: %v", err)
		}
		proxyPID = cmd.Process.Pid
		// Detach so it survives after we exit
		cmd.Process.Release()

		// Wait for the proxy to be ready
		time.Sleep(500 * time.Millisecond)

		l.Set("proxy-server", fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
		ignoreCertErrors = true // Proxy requires ignoring cert errors
		fmt.Fprintf(w, "Auth proxy started (PID %d, port %d) -> %s\n", proxyPID, proxyPort, server)
	}

	if ignoreCertErrors {
		l.Set("ignore-certificate-errors")
	}

	// Suppress rod's default stderr logging
	l.Set("silent-launch")

	debugURL := l.MustLaunch()

	// Get Chrome PID from the launcher
	pid := l.PID()

	state := &State{
		DebugURL:   debugURL,
		ChromePID:  pid,
		ActivePage: 0,
		DataDir:    dataDir,
		ProxyPID:   proxyPID,
		ProxyPort:  proxyPort,
	}

	if err := saveState(state); err != nil {
		fatal("failed to save state: %v", err)
	}

	fmt.Fprintf(w, "Chrome started (PID %d)\n", pid)
	fmt.Fprintf(w, "Debug URL: %s\n", debugURL)

	browser, err := connectBrowser(state)
	if err != nil {
		fatal("failed to connect to Chrome: %v", err)
	}

	return state, browser
}

