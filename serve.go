package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-rod/rod"
)

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

func cmdServe(args []string) {
	headless := true
	ignoreCertErrors := false
	for _, arg := range args {
		switch arg {
		case "--show":
			headless = false
		case "--insecure", "-k":
			ignoreCertErrors = true
		default:
			fatal("unknown flag: %s\nusage: rodney serve [--show] [--insecure]", arg)
		}
	}

	state, browser := launchChrome(headless, ignoreCertErrors, false, io.Discard)

	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for large requests
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Send ready message
	enc.Encode(serveResponse{OK: true})

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req serveRequest
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(serveResponse{
				OK:       false,
				ExitCode: 2,
				Stderr:   fmt.Sprintf("invalid JSON: %v", err),
			})
			continue
		}

		resp := dispatchServe(browser, state, req)
		enc.Encode(resp)

		// Stop command means graceful shutdown
		if req.Cmd == "stop" {
			break
		}
	}

	// stdin closed or stop received — clean up Chrome
	browser.MustClose()
}

// dispatchServe routes a serve request to the appropriate runXxx function.
func dispatchServe(browser *rod.Browser, state *State, req serveRequest) serveResponse {
	resp := serveResponse{ID: req.ID}

	// Helper to get the active page with timeout
	getPage := func() (*rod.Page, error) {
		page, err := getActivePage(browser, state)
		if err != nil {
			return nil, err
		}
		return page.Timeout(defaultTimeout), nil
	}

	// Recover from panics (rod Must* functions)
	defer func() {
		if r := recover(); r != nil {
			resp.OK = false
			resp.ExitCode = 2
			resp.Stderr = fmt.Sprintf("error: %v", r)
		}
	}()

	switch req.Cmd {
	// Navigation
	case "open":
		out, err := runOpen(browser, state, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out
		// Save state in-memory (already mutated by runOpen)

	case "back":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runBack(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "forward":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runForward(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "reload":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runReload(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "clear-cache":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runClearCache(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Page info
	case "url":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runURL(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "title":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runTitle(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "html":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runHTML(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "text":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runText(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "attr":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runAttr(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "pdf":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runPDF(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Interaction
	case "js":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runJS(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "click":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runClick(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "input":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runInput(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "clear":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runClear(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "select":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runSelect(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "submit":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runSubmit(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "hover":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runHover(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "focus":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runFocus(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "file":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runFile(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "download":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runDownload(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Waiting
	case "wait":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runWait(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "waitload":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runWaitLoad(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "waitstable":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runWaitStable(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "waitidle":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runWaitIdle(page)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "sleep":
		out, err := runSleep(req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Screenshots
	case "screenshot":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runScreenshot(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "screenshot-el":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runScreenshotEl(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Tabs
	case "pages":
		out, err := runPages(browser, state)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "page":
		out, err := runPage(browser, state, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "newpage":
		out, err := runNewPage(browser, state, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "closepage":
		out, err := runClosePage(browser, state, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Element checks (exit-code commands)
	case "exists":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, exitCode, err := runExists(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = exitCode == 0
		resp.Stdout = out
		resp.ExitCode = exitCode

	case "count":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runCount(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "visible":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, exitCode, err := runVisible(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = exitCode == 0
		resp.Stdout = out
		resp.ExitCode = exitCode

	case "assert":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, exitCode, err := runAssert(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = exitCode == 0
		resp.Stdout = out
		resp.ExitCode = exitCode

	// Accessibility
	case "ax-tree":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runAXTree(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	case "ax-find":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, exitCode, err := runAXFind(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = exitCode == 0
		resp.Stdout = out
		resp.ExitCode = exitCode

	case "ax-node":
		page, err := getPage()
		if err != nil {
			return errorResponse(req.ID, err)
		}
		out, err := runAXNode(page, req.Args)
		if err != nil {
			return errorResponse(req.ID, err)
		}
		resp.OK = true
		resp.Stdout = out

	// Lifecycle
	case "stop":
		resp.OK = true
		resp.Stdout = "stopping"

	default:
		resp.OK = false
		resp.ExitCode = 2
		resp.Stderr = fmt.Sprintf("unknown command: %s", req.Cmd)
	}

	return resp
}

func errorResponse(id int, err error) serveResponse {
	return serveResponse{
		ID:       id,
		OK:       false,
		ExitCode: 2,
		Stderr:   strings.TrimPrefix(err.Error(), "error: "),
	}
}
