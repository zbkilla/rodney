package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// testEnv holds a shared browser and test HTTP server for all tests.
type testEnv struct {
	browser *rod.Browser
	server  *httptest.Server
}

var env *testEnv

func TestMain(m *testing.M) {
	// Launch headless Chrome once for all tests
	l := launcher.New().
		Set("no-sandbox").
		Set("disable-gpu").
		Set("single-process").
		Headless(true).
		Leakless(false)

	if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
		l = l.Bin(bin)
	}

	u := l.MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()

	// Start test HTTP server with known HTML fixtures
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/form", handleForm)
	mux.HandleFunc("/upload", handleUpload)
	mux.HandleFunc("/download", handleDownload)
	mux.HandleFunc("/testfile.txt", handleTestFile)
	mux.HandleFunc("/empty", handleEmpty)
	mux.HandleFunc("/spa", handleSPA)
	server := httptest.NewServer(mux)

	env = &testEnv{browser: browser, server: server}

	code := m.Run()

	server.Close()
	browser.MustClose()
	os.Exit(code)
}

// --- HTML fixtures ---

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Test Page</title></head>
<body>
  <nav aria-label="Main">
    <a href="/about">About</a>
    <a href="/contact">Contact</a>
  </nav>
  <main>
    <h1>Welcome</h1>
    <p>Hello world</p>
    <button id="submit-btn">Submit</button>
    <button id="cancel-btn" disabled>Cancel</button>
  </main>
</body>
</html>`))
}

func handleForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Form Page</title></head>
<body>
  <h1>Contact Us</h1>
  <form>
    <label for="name-input">Name</label>
    <input id="name-input" type="text" aria-required="true">
    <label for="email-input">Email</label>
    <input id="email-input" type="email">
    <select id="topic" aria-label="Topic">
      <option value="general">General</option>
      <option value="support">Support</option>
    </select>
    <button type="submit">Send</button>
  </form>
</body>
</html>`))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Upload Page</title></head>
<body>
  <input id="file-input" type="file" accept="image/*">
  <span id="file-name"></span>
  <script>
    document.getElementById('file-input').addEventListener('change', function(e) {
      document.getElementById('file-name').textContent = e.target.files[0] ? e.target.files[0].name : '';
    });
  </script>
</body>
</html>`))
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Download Page</title></head>
<body>
  <a id="file-link" href="/testfile.txt">Download file</a>
  <a id="data-link" href="data:text/plain;base64,SGVsbG8gV29ybGQ=">Download data</a>
  <img id="test-img" src="/testfile.txt">
</body>
</html>`))
}

func handleTestFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Hello World"))
}

func handleEmpty(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Empty Page</title></head>
<body></body>
</html>`))
}

// --- Helper: navigate to a fixture and return the page ---

func navigateTo(t *testing.T, path string) *rod.Page {
	t.Helper()
	page := env.browser.MustPage(env.server.URL + path)
	page.MustWaitLoad()
	t.Cleanup(func() { page.MustClose() })
	return page
}

// =====================
// ax-tree tests (RED)
// =====================

func TestAXTree_ReturnsNodes(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	// Sanity: we should get nodes back
	if len(result.Nodes) == 0 {
		t.Fatal("expected nodes in accessibility tree, got 0")
	}

	// Now test our formatting function
	out := formatAXTree(result.Nodes)
	if out == "" {
		t.Fatal("formatAXTree returned empty string")
	}
	if !strings.Contains(out, "Welcome") {
		t.Errorf("tree should contain heading text 'Welcome', got:\n%s", out)
	}
	if !strings.Contains(out, "button") {
		t.Errorf("tree should contain 'button' role, got:\n%s", out)
	}
	if !strings.Contains(out, "Submit") {
		t.Errorf("tree should contain button name 'Submit', got:\n%s", out)
	}
}

func TestAXTree_Indentation(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTree(result.Nodes)
	lines := strings.Split(out, "\n")

	// Root node should have no indentation
	if len(lines) == 0 {
		t.Fatal("no lines in output")
	}
	if strings.HasPrefix(lines[0], " ") {
		t.Errorf("root node should not be indented, got: %q", lines[0])
	}

	// Some lines should be indented (children)
	hasIndented := false
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") {
			hasIndented = true
			break
		}
	}
	if !hasIndented {
		t.Errorf("expected some indented lines for child nodes, got:\n%s", out)
	}
}

func TestAXTree_SkipsIgnoredNodes(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTree(result.Nodes)

	// Count ignored vs total
	ignoredCount := 0
	for _, node := range result.Nodes {
		if node.Ignored {
			ignoredCount++
		}
	}

	// If there are ignored nodes, they shouldn't appear in text output
	if ignoredCount > 0 {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) >= len(result.Nodes) {
			t.Errorf("text output should skip ignored nodes: %d lines for %d nodes (%d ignored)",
				len(lines), len(result.Nodes), ignoredCount)
		}
	}
}

func TestAXTree_DepthLimit(t *testing.T) {
	page := navigateTo(t, "/")
	full, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}

	depth := 2
	limited, err := proto.AccessibilityGetFullAXTree{Depth: &depth}.Call(page)
	if err != nil {
		t.Fatalf("CDP call with depth failed: %v", err)
	}

	if len(limited.Nodes) >= len(full.Nodes) {
		t.Errorf("depth-limited tree (%d nodes) should have fewer nodes than full tree (%d nodes)",
			len(limited.Nodes), len(full.Nodes))
	}
}

func TestAXTree_JSONOutput(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTreeJSON(result.Nodes)
	// Must be valid JSON
	var parsed []interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\nOutput:\n%s", err, out[:min(len(out), 500)])
	}
	if len(parsed) == 0 {
		t.Error("JSON output should contain nodes")
	}
}

// =====================
// ax-find tests (RED)
// =====================

func TestAXFind_ByRole(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "", "button")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 buttons, got %d", len(nodes))
	}

	out := formatAXNodeList(nodes)
	if !strings.Contains(out, "Submit") {
		t.Errorf("output should contain 'Submit' button, got:\n%s", out)
	}
	if !strings.Contains(out, "Cancel") {
		t.Errorf("output should contain 'Cancel' button, got:\n%s", out)
	}
}

func TestAXFind_ByName(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "Submit", "")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 node named 'Submit', got 0")
	}
	out := formatAXNodeList(nodes)
	if !strings.Contains(out, "Submit") {
		t.Errorf("output should contain 'Submit', got:\n%s", out)
	}
}

func TestAXFind_ByNameAndRoleExact(t *testing.T) {
	page := navigateTo(t, "/")
	// Combining name + role should give exactly one result
	nodes, err := queryAXNodes(page, "Submit", "button")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 button named 'Submit', got %d", len(nodes))
	}
}

func TestAXFind_ByNameAndRole(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "About", "link")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 link named 'About', got %d", len(nodes))
	}
}

func TestAXFind_NoResults(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "NonexistentThing", "")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 results for nonexistent name, got %d", len(nodes))
	}
}

func TestAXFind_FormPage(t *testing.T) {
	page := navigateTo(t, "/form")
	nodes, err := queryAXNodes(page, "", "textbox")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 textboxes on form page, got %d", len(nodes))
	}
}

// =====================
// ax-node tests (RED)
// =====================

func TestAXNode_ButtonBySelector(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#submit-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "button") {
		t.Errorf("should show role 'button', got:\n%s", out)
	}
	if !strings.Contains(out, "Submit") {
		t.Errorf("should show name 'Submit', got:\n%s", out)
	}
}

func TestAXNode_DisabledButton(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#cancel-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "button") {
		t.Errorf("should show role 'button', got:\n%s", out)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("should show disabled property, got:\n%s", out)
	}
}

func TestAXNode_InputWithLabel(t *testing.T) {
	page := navigateTo(t, "/form")
	node, err := getAXNode(page, "#name-input")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "textbox") {
		t.Errorf("should show role 'textbox', got:\n%s", out)
	}
	if !strings.Contains(out, "Name") {
		t.Errorf("should show accessible name 'Name' from label, got:\n%s", out)
	}
}

func TestAXNode_HeadingLevel(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "h1")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "heading") {
		t.Errorf("should show role 'heading', got:\n%s", out)
	}
	if !strings.Contains(out, "level") {
		t.Errorf("should show level property for heading, got:\n%s", out)
	}
}

func TestAXNode_JSONOutput(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#submit-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetailJSON(node)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\nOutput:\n%s", err, out)
	}
	if _, ok := parsed["nodeId"]; !ok {
		t.Error("JSON should contain nodeId field")
	}
}

func TestAXNode_SelectorNotFound(t *testing.T) {
	page := navigateTo(t, "/")
	// Use a short timeout so we don't block for 30s waiting for a nonexistent element
	shortPage := page.Timeout(2 * time.Second)
	_, err := getAXNode(shortPage, "#does-not-exist")
	if err == nil {
		t.Error("expected error for nonexistent selector, got nil")
	}
}

// =====================
// file command tests
// =====================

func TestFile_SetFileOnInput(t *testing.T) {
	page := navigateTo(t, "/upload")

	// Create a temp file to upload
	tmp, err := os.CreateTemp("", "rodney-test-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.Write([]byte("test content"))
	tmp.Close()

	el, err := page.Element("#file-input")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	if err := el.SetFiles([]string{tmp.Name()}); err != nil {
		t.Fatalf("SetFiles failed: %v", err)
	}

	// Wait for the change event to fire and check the file name
	page.MustWaitStable()
	nameEl, err := page.Element("#file-name")
	if err != nil {
		t.Fatalf("file-name element not found: %v", err)
	}
	text, _ := nameEl.Text()
	if text == "" {
		t.Error("expected file name to be set after SetFiles, got empty string")
	}
}

func TestFile_MultipleFiles(t *testing.T) {
	page := navigateTo(t, "/upload")

	tmp1, _ := os.CreateTemp("", "rodney-test1-*.txt")
	defer os.Remove(tmp1.Name())
	tmp1.Write([]byte("file 1"))
	tmp1.Close()

	tmp2, _ := os.CreateTemp("", "rodney-test2-*.txt")
	defer os.Remove(tmp2.Name())
	tmp2.Write([]byte("file 2"))
	tmp2.Close()

	el, err := page.Element("#file-input")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}

	// Setting files should not error even with multiple files
	if err := el.SetFiles([]string{tmp1.Name(), tmp2.Name()}); err != nil {
		t.Fatalf("SetFiles with multiple files failed: %v", err)
	}
}

// =====================
// download command tests
// =====================

func TestDownload_DataURL(t *testing.T) {
	// Test decoding a data: URL directly
	data, err := decodeDataURL("data:text/plain;base64,SGVsbG8gV29ybGQ=")
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_DataURL_URLEncoded(t *testing.T) {
	data, err := decodeDataURL("data:text/plain,Hello%20World")
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_InferFilename_URL(t *testing.T) {
	name := inferDownloadFilename("https://example.com/images/photo.png")
	if name != "photo.png" {
		t.Errorf("expected 'photo.png', got %q", name)
	}
}

func TestDownload_InferFilename_DataURL(t *testing.T) {
	name := inferDownloadFilename("data:image/png;base64,abc")
	if !strings.HasPrefix(name, "download") || !strings.Contains(name, ".png") {
		t.Errorf("expected 'download*.png', got %q", name)
	}
}

func TestDownload_FetchLink(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#file-link")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	href := el.MustAttribute("href")
	if href == nil {
		t.Fatal("expected href attribute")
	}

	// Fetch using JS in the page context, same as cmdDownload does
	js := fmt.Sprintf(`async () => {
		const resp = await fetch(%q);
		if (!resp.ok) throw new Error('HTTP ' + resp.status);
		const buf = await resp.arrayBuffer();
		const bytes = new Uint8Array(buf);
		let binary = '';
		for (let i = 0; i < bytes.length; i++) {
			binary += String.fromCharCode(bytes[i]);
		}
		return btoa(binary);
	}`, *href)
	result, err := page.Eval(js)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	data, err := base64.StdEncoding.DecodeString(result.Value.Str())
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_DataLinkElement(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#data-link")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	href := el.MustAttribute("href")
	if href == nil {
		t.Fatal("expected href attribute")
	}

	data, err := decodeDataURL(*href)
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_ImgSrc(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#test-img")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	src := el.MustAttribute("src")
	if src == nil {
		t.Fatal("expected src attribute")
	}
	if *src != "/testfile.txt" {
		t.Errorf("expected '/testfile.txt', got %q", *src)
	}
}

// =====================
// SPA hash routing tests
// =====================

func handleSPA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>SPA App</title></head>
<body>
  <nav>
    <a href="#home" id="nav-home">Home</a>
    <a href="#page2" id="nav-page2">Page 2</a>
    <a href="#page3" id="nav-page3">Page 3</a>
  </nav>
  <div id="content"></div>
  <script>
    var pages = {
      home:  '<h1>Home Page</h1><p>Welcome to the SPA</p>',
      page2: '<h1>Page Two</h1><p>This is the second page</p>',
      page3: '<h1>Page Three</h1><p>This is the third page</p>'
    };
    function route() {
      var hash = location.hash.replace('#', '') || 'home';
      document.getElementById('content').innerHTML = pages[hash] || '<h1>Not Found</h1>';
      document.title = 'SPA - ' + hash;
    }
    window.addEventListener('hashchange', route);
    route();
  </script>
</body>
</html>`))
}

func TestSPA_OpenWithFragment(t *testing.T) {
	// Open the SPA directly at #page2 and verify the correct content renders
	page := env.browser.MustPage(env.server.URL + "/spa#page2")
	page.MustWaitLoad()
	t.Cleanup(func() { page.MustClose() })

	// Wait for the JS router to execute
	page.MustWaitStable()

	// Verify the URL contains the fragment
	info, err := page.Info()
	if err != nil {
		t.Fatalf("failed to get page info: %v", err)
	}
	if !strings.HasSuffix(info.URL, "/spa#page2") {
		t.Errorf("expected URL ending with /spa#page2, got %s", info.URL)
	}

	// Verify the title was set by the SPA router
	if info.Title != "SPA - page2" {
		t.Errorf("expected title 'SPA - page2', got %q", info.Title)
	}

	// Verify the correct content is rendered
	h1, err := page.Element("h1")
	if err != nil {
		t.Fatalf("h1 not found: %v", err)
	}
	text, _ := h1.Text()
	if text != "Page Two" {
		t.Errorf("expected h1 'Page Two', got %q", text)
	}
}

func TestSPA_OpenWithFragment_Page3(t *testing.T) {
	// Open the SPA directly at #page3
	page := env.browser.MustPage(env.server.URL + "/spa#page3")
	page.MustWaitLoad()
	t.Cleanup(func() { page.MustClose() })

	page.MustWaitStable()

	info, err := page.Info()
	if err != nil {
		t.Fatalf("failed to get page info: %v", err)
	}
	if info.Title != "SPA - page3" {
		t.Errorf("expected title 'SPA - page3', got %q", info.Title)
	}

	h1, err := page.Element("h1")
	if err != nil {
		t.Fatalf("h1 not found: %v", err)
	}
	text, _ := h1.Text()
	if text != "Page Three" {
		t.Errorf("expected h1 'Page Three', got %q", text)
	}
}

func TestSPA_DefaultRoute(t *testing.T) {
	// Open the SPA without a fragment - should default to home
	page := navigateTo(t, "/spa")
	page.MustWaitStable()

	info, err := page.Info()
	if err != nil {
		t.Fatalf("failed to get page info: %v", err)
	}
	if info.Title != "SPA - home" {
		t.Errorf("expected title 'SPA - home', got %q", info.Title)
	}

	h1, err := page.Element("h1")
	if err != nil {
		t.Fatalf("h1 not found: %v", err)
	}
	text, _ := h1.Text()
	if text != "Home Page" {
		t.Errorf("expected h1 'Home Page', got %q", text)
	}
}

func TestSPA_NavigateByClick(t *testing.T) {
	// Start on the default home page, then click to navigate to page2
	page := navigateTo(t, "/spa")
	page.MustWaitStable()

	// Click the Page 2 nav link
	nav2 := page.MustElement("#nav-page2")
	nav2.MustClick()

	// Wait for the hash change to take effect
	page.MustWaitStable()

	// Verify URL updated
	info, err := page.Info()
	if err != nil {
		t.Fatalf("failed to get page info: %v", err)
	}
	if !strings.HasSuffix(info.URL, "/spa#page2") {
		t.Errorf("expected URL ending with /spa#page2, got %s", info.URL)
	}

	// Verify content changed
	h1, err := page.Element("h1")
	if err != nil {
		t.Fatalf("h1 not found: %v", err)
	}
	text, _ := h1.Text()
	if text != "Page Two" {
		t.Errorf("expected h1 'Page Two' after click, got %q", text)
	}

	// Verify title updated
	if info.Title != "SPA - page2" {
		t.Errorf("expected title 'SPA - page2', got %q", info.Title)
	}
}

func TestSPA_NavigateMultiplePages(t *testing.T) {
	// Navigate through multiple SPA pages by clicking
	page := navigateTo(t, "/spa")
	page.MustWaitStable()

	// Go to page2
	page.MustElement("#nav-page2").MustClick()
	page.MustWaitStable()
	h1, _ := page.Element("h1")
	text, _ := h1.Text()
	if text != "Page Two" {
		t.Errorf("expected 'Page Two', got %q", text)
	}

	// Go to page3
	page.MustElement("#nav-page3").MustClick()
	page.MustWaitStable()
	h1, _ = page.Element("h1")
	text, _ = h1.Text()
	if text != "Page Three" {
		t.Errorf("expected 'Page Three', got %q", text)
	}

	// Go back to home
	page.MustElement("#nav-home").MustClick()
	page.MustWaitStable()
	h1, _ = page.Element("h1")
	text, _ = h1.Text()
	if text != "Home Page" {
		t.Errorf("expected 'Home Page', got %q", text)
	}
}

func TestSPA_JSHashNavigation(t *testing.T) {
	// Navigate by setting location.hash via JS (simulates programmatic navigation)
	page := navigateTo(t, "/spa")
	page.MustWaitStable()

	// Change hash via JavaScript
	page.MustEval(`() => { location.hash = 'page3'; }`)
	page.MustWaitStable()

	info, err := page.Info()
	if err != nil {
		t.Fatalf("failed to get page info: %v", err)
	}
	if !strings.HasSuffix(info.URL, "/spa#page3") {
		t.Errorf("expected URL ending with /spa#page3, got %s", info.URL)
	}

	h1, err := page.Element("h1")
	if err != nil {
		t.Fatalf("h1 not found: %v", err)
	}
	text, _ := h1.Text()
	if text != "Page Three" {
		t.Errorf("expected 'Page Three' after JS navigation, got %q", text)
	}
}

func TestMimeToExt(t *testing.T) {
	tests := []struct {
		mime string
		ext  string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"application/pdf", ".pdf"},
		{"text/plain", ".txt"},
		{"unknown/type", ""},
	}
	for _, tt := range tests {
		got := mimeToExt(tt.mime)
		if got != tt.ext {
			t.Errorf("mimeToExt(%q) = %q, want %q", tt.mime, got, tt.ext)
		}
	}
}
