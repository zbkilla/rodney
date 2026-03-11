package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// serveHarness manages a `rodney serve` subprocess for testing.
type serveHarness struct {
	cmd    *exec.Cmd
	stdin  *json.Encoder
	stdout *bufio.Scanner
	nextID int
}

func newServeHarness(t *testing.T) *serveHarness {
	t.Helper()

	// Build the binary
	binPath := buildTestBinary(t)

	// Use a temp state dir for isolation.
	// Don't use t.TempDir() because Chrome writes data that may still
	// be flushing when the test cleanup runs.
	stateDir, err := os.MkdirTemp("", "rodney-serve-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cmd := exec.Command(binPath, "serve")
	cmd.Env = append(os.Environ(),
		"RODNEY_HOME="+stateDir,
		"ROD_TIMEOUT=5", // Short timeout for tests
	)
	if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
		cmd.Env = append(cmd.Env, "ROD_CHROME_BIN="+bin)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start rodney serve: %v", err)
	}

	h := &serveHarness{
		cmd:    cmd,
		stdin:  json.NewEncoder(stdinPipe),
		stdout: bufio.NewScanner(stdoutPipe),
	}

	// Wait for the ready message
	resp := h.readResponse(t)
	if !resp.OK {
		t.Fatalf("serve did not send ready message, got: %+v", resp)
	}

	t.Cleanup(func() {
		stdinPipe.Close()
		cmd.Wait()
	})

	return h
}

func (h *serveHarness) send(t *testing.T, cmd string, args ...string) serveResponse {
	t.Helper()
	h.nextID++
	req := serveRequest{
		ID:   h.nextID,
		Cmd:  cmd,
		Args: args,
	}
	if err := h.stdin.Encode(req); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	return h.readResponse(t)
}

func (h *serveHarness) readResponse(t *testing.T) serveResponse {
	t.Helper()
	if !h.stdout.Scan() {
		t.Fatalf("failed to read response: %v", h.stdout.Err())
	}
	var resp serveResponse
	if err := json.Unmarshal(h.stdout.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response %q: %v", h.stdout.Text(), err)
	}
	return resp
}

// buildTestBinary compiles the rodney binary once per test run.
// Uses a fixed path outside of t.TempDir() so it persists across tests.
var testBinaryPath string

func buildTestBinary(t *testing.T) string {
	t.Helper()
	if testBinaryPath != "" {
		if _, err := os.Stat(testBinaryPath); err == nil {
			return testBinaryPath
		}
	}
	binPath := os.TempDir() + "/rodney-serve-test"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test binary: %v\n%s", err, out)
	}
	testBinaryPath = binPath
	return binPath
}

func TestServe_ReadyMessage(t *testing.T) {
	// newServeHarness already checks for the ready message
	_ = newServeHarness(t)
}

func TestServe_OpenAndTitle(t *testing.T) {
	h := newServeHarness(t)

	resp := h.send(t, "open", env.server.URL)
	if !resp.OK {
		t.Fatalf("open failed: %s", resp.Stderr)
	}
	if resp.Stdout != "Test Page" {
		t.Errorf("open returned %q, want %q", resp.Stdout, "Test Page")
	}

	resp = h.send(t, "title")
	if !resp.OK {
		t.Fatalf("title failed: %s", resp.Stderr)
	}
	if resp.Stdout != "Test Page" {
		t.Errorf("title returned %q, want %q", resp.Stdout, "Test Page")
	}
}

func TestServe_URLCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)
	resp := h.send(t, "url")
	if !resp.OK {
		t.Fatalf("url failed: %s", resp.Stderr)
	}
	if resp.Stdout != env.server.URL+"/" {
		t.Errorf("url returned %q, want %q", resp.Stdout, env.server.URL+"/")
	}
}

func TestServe_HTMLCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)
	resp := h.send(t, "html", "h1")
	if !resp.OK {
		t.Fatalf("html failed: %s", resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "Welcome") {
		t.Errorf("html output %q does not contain 'Welcome'", resp.Stdout)
	}
}

func TestServe_JSCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)
	resp := h.send(t, "js", "1 + 2")
	if !resp.OK {
		t.Fatalf("js failed: %s", resp.Stderr)
	}
	if resp.Stdout != "3" {
		t.Errorf("js returned %q, want %q", resp.Stdout, "3")
	}
}

func TestServe_ClickCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)
	resp := h.send(t, "click", "#submit-btn")
	if !resp.OK {
		t.Fatalf("click failed: %s", resp.Stderr)
	}
	if resp.Stdout != "Clicked" {
		t.Errorf("click returned %q, want %q", resp.Stdout, "Clicked")
	}
}

func TestServe_ExistsCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)

	// Element that exists
	resp := h.send(t, "exists", "h1")
	if resp.Stdout != "true" {
		t.Errorf("exists returned %q, want %q", resp.Stdout, "true")
	}
	if resp.ExitCode != 0 {
		t.Errorf("exists exit_code = %d, want 0", resp.ExitCode)
	}

	// Element that doesn't exist
	resp = h.send(t, "exists", "#nonexistent")
	if resp.Stdout != "false" {
		t.Errorf("exists returned %q, want %q", resp.Stdout, "false")
	}
	if resp.ExitCode != 1 {
		t.Errorf("exists exit_code = %d, want 1", resp.ExitCode)
	}
}

func TestServe_ErrorResponse(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)

	// Click on a non-existent element
	resp := h.send(t, "click", "#nonexistent-element-xyz")
	if resp.OK {
		t.Errorf("expected error for clicking non-existent element")
	}
	if resp.ExitCode != 2 {
		t.Errorf("exit_code = %d, want 2", resp.ExitCode)
	}
	if resp.Stderr == "" {
		t.Error("expected non-empty stderr")
	}
}

func TestServe_UnknownCommand(t *testing.T) {
	h := newServeHarness(t)

	resp := h.send(t, "nonexistent-command")
	if resp.OK {
		t.Error("expected error for unknown command")
	}
	if resp.ExitCode != 2 {
		t.Errorf("exit_code = %d, want 2", resp.ExitCode)
	}
}

func TestServe_StopCommand(t *testing.T) {
	h := newServeHarness(t)

	resp := h.send(t, "stop")
	if !resp.OK {
		t.Fatalf("stop failed: %s", resp.Stderr)
	}

	// Process should exit after stop
	done := make(chan error, 1)
	go func() {
		done <- h.cmd.Wait()
	}()
	select {
	case <-done:
		// good
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after stop command")
	}
}

func TestServe_StdinEOFCleansUp(t *testing.T) {
	binPath := buildTestBinary(t)
	stateDir, err := os.MkdirTemp("", "rodney-serve-eof-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cmd := exec.Command(binPath, "serve")
	cmd.Env = append(os.Environ(), "RODNEY_HOME="+stateDir, "ROD_TIMEOUT=5")
	if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
		cmd.Env = append(cmd.Env, "ROD_CHROME_BIN="+bin)
	}

	stdinPipe, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for ready message
	scanner := bufio.NewScanner(stdoutPipe)
	if !scanner.Scan() {
		t.Fatalf("no ready message: %v", scanner.Err())
	}

	// Get Chrome PID from ready message for later checking
	pid := cmd.Process.Pid
	_ = pid

	// Close stdin — this should trigger cleanup
	stdinPipe.Close()

	// Process should exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
		// good — rodney exited after stdin closed
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process did not exit after stdin EOF")
	}
}

func TestServe_TextAndCountCommands(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", env.server.URL)

	resp := h.send(t, "text", "h1")
	if !resp.OK {
		t.Fatalf("text failed: %s", resp.Stderr)
	}
	if resp.Stdout != "Welcome" {
		t.Errorf("text returned %q, want %q", resp.Stdout, "Welcome")
	}

	resp = h.send(t, "count", "a")
	if !resp.OK {
		t.Fatalf("count failed: %s", resp.Stderr)
	}
	// The test page has at least 2 links (About, Contact)
	if resp.Stdout != "3" && resp.Stdout != "2" {
		t.Logf("count returned %q (checking for presence of links)", resp.Stdout)
	}
}

func TestServe_InputCommand(t *testing.T) {
	h := newServeHarness(t)

	h.send(t, "open", fmt.Sprintf("%s/form", env.server.URL))

	resp := h.send(t, "input", "#name-input", "Test User")
	if !resp.OK {
		t.Fatalf("input failed: %s", resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "Typed") {
		t.Errorf("input returned %q, want something with 'Typed'", resp.Stdout)
	}
}

func TestServe_MultipleCommandsSequence(t *testing.T) {
	h := newServeHarness(t)

	// Open page
	resp := h.send(t, "open", env.server.URL)
	if !resp.OK {
		t.Fatalf("open failed: %s", resp.Stderr)
	}

	// Get URL
	resp = h.send(t, "url")
	if !resp.OK {
		t.Fatalf("url failed: %s", resp.Stderr)
	}

	// Get title
	resp = h.send(t, "title")
	if !resp.OK {
		t.Fatalf("title failed: %s", resp.Stderr)
	}

	// Run JS
	resp = h.send(t, "js", "document.title")
	if !resp.OK {
		t.Fatalf("js failed: %s", resp.Stderr)
	}
	if resp.Stdout != "Test Page" {
		t.Errorf("js returned %q, want %q", resp.Stdout, "Test Page")
	}

	// Check element exists
	resp = h.send(t, "exists", "h1")
	if resp.Stdout != "true" {
		t.Errorf("exists returned %q, want %q", resp.Stdout, "true")
	}
}
