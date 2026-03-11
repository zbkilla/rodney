# rodney-py: Python Library Demo

*2026-03-11T19:39:57Z by Showboat 0.6.1*
<!-- showboat-id: dc961878-7aee-42d8-82a4-c65500b3b5ba -->

The rodney-py library provides sync and async Python clients for Chrome automation, powered by the `rodney serve` JSON-over-stdio protocol. Let's explore both APIs against a real browser.

## Setup

First, let's verify rodney is on the PATH and the Python package is importable.

```bash
rodney --version
```

```output
dev
```

```bash
cd python && uv run python -c 'import rodney; print(rodney.__all__)'
```

```output
['Browser', 'AsyncBrowser', 'RodneyError', 'CheckFailed', 'RunResult']
```

## Sync Browser

The `Browser` class is the simplest way to use rodney from Python. It launches Chrome via `rodney serve` as a subprocess and communicates over JSON pipes. The context manager ensures Chrome is cleaned up even if an exception occurs.

### Basic navigation and page info

```bash
cd python && uv run python3 /tmp/demo_basic.py
```

```output
Title: Example Domain
URL:   https://www.example.com/
Text:  Example Domain
```

### JavaScript evaluation

The `js()` method evaluates JavaScript and returns native Python types — numbers, strings, booleans, lists, and dicts are all automatically parsed.

```bash
cd python && uv run python3 /tmp/demo_js.py
```

```output
Number: 3
String: Example Domain
Boolean: True
Array: [1, 2, 3]
Null: None
```

### Element checks

`exists()` and `visible()` return booleans. `count()` returns an integer.

```bash
cd python && uv run python3 /tmp/demo_checks.py
```

```output
h1 exists: True
#nope exists: False
p count: 2
h1 visible: True
```

### Form interaction

Click, type, and read values back — all through the same Browser instance.

```bash
cd python && uv run python3 /tmp/demo_form.py
```

```output
Page: Form Demo
Name field: Alice
Color field: blue
After clear: None
```

### Error handling

Errors from rodney (exit code 2) are raised as `RodneyError`. This happens when you try to interact with elements that don't exist, for example.

```bash
cd python && uv run python3 /tmp/demo_errors.py
```

```output
Missing element exists: False
RodneyError: element not found: context deadline exceeded
```

### HTML extraction

Pull outer HTML of elements or the full page.

```bash
cd python && uv run python3 /tmp/demo_html.py
```

```output
Element HTML: <h1>Example Domain</h1>
Title attr: https://iana.org/domains/example
```

## Async Browser

`AsyncBrowser` has the identical API but uses `async`/`await`. It's ideal for asyncio applications and enables running multiple browser instances concurrently.

### Basic async usage

```bash
cd python && uv run python3 /tmp/demo_async_basic.py
```

```output
Title: Example Domain
URL:   https://www.example.com/
h1:    Example Domain
JS:    1024
```

### Concurrent browsers

Each `AsyncBrowser` instance gets its own Chrome process. With `asyncio.gather`, you can run multiple browsers concurrently — while one waits on network I/O, others make progress.

```bash
cd python && uv run python3 /tmp/demo_concurrent.py
```

```output
https://www.example.com -> Example Domain
https://www.example.org -> Example Domain
https://www.example.net -> Example Domain

3 browsers concurrently in 3.2s
```

### Alternative creation: `AsyncBrowser.start()`

For cases where you need the browser to outlive a single `async with` block, use the classmethod factory.

```bash
cd python && uv run python3 /tmp/demo_factory.py
```

```output
Title: Example Domain
Running: True
After stop: False
```

## Crash Safety

The key advantage of `rodney serve` over subprocess-per-command: Chrome cleanup is guaranteed by the OS. When the Python process dies (even via SIGKILL), the stdin pipe closes, rodney reads EOF, and kills Chrome. No orphan processes.

```bash
cd python && uv run python3 /tmp/demo_crash.py
```

```output
Child Python PID: 13699
Rodney serve PID: 13700
Python killed with SIGKILL
Rodney PID 13700: cleaned up (good)
```

## Test Suite

The library ships with comprehensive tests for both sync and async APIs, covering navigation, page info, interaction, element checks, error handling, and lifecycle management.

```bash
cd python && uv run pytest tests/ -v --tb=short 2>&1 | head -50
```

```output
============================= test session starts ==============================
platform linux -- Python 3.11.14, pytest-9.0.2, pluggy-1.6.0 -- /home/user/rodney/python/.venv/bin/python3
cachedir: .pytest_cache
rootdir: /home/user/rodney/python
configfile: pyproject.toml
plugins: asyncio-1.3.0
asyncio: mode=Mode.AUTO, debug=False, asyncio_default_fixture_loop_scope=None, asyncio_default_test_loop_scope=function
collecting ... collected 32 items

tests/test_async_browser.py::TestAsyncBrowserLifecycle::test_async_context_manager PASSED [  3%]
tests/test_async_browser.py::TestAsyncBrowserLifecycle::test_stop_is_idempotent PASSED [  6%]
tests/test_async_browser.py::TestAsyncNavigation::test_open_returns_title PASSED [  9%]
tests/test_async_browser.py::TestAsyncNavigation::test_url PASSED        [ 12%]
tests/test_async_browser.py::TestAsyncPageInfo::test_html PASSED         [ 15%]
tests/test_async_browser.py::TestAsyncPageInfo::test_text PASSED         [ 18%]
tests/test_async_browser.py::TestAsyncInteraction::test_click PASSED     [ 21%]
tests/test_async_browser.py::TestAsyncInteraction::test_input PASSED     [ 25%]
tests/test_async_browser.py::TestAsyncInteraction::test_js PASSED        [ 28%]
tests/test_async_browser.py::TestAsyncElementChecks::test_exists_true PASSED [ 31%]
tests/test_async_browser.py::TestAsyncElementChecks::test_exists_false PASSED [ 34%]
tests/test_async_browser.py::TestAsyncElementChecks::test_count PASSED   [ 37%]
tests/test_async_browser.py::TestAsyncElementChecks::test_visible_true PASSED [ 40%]
tests/test_async_browser.py::TestAsyncElementChecks::test_visible_false PASSED [ 43%]
tests/test_async_browser.py::TestAsyncErrors::test_error_raises_rodney_error PASSED [ 46%]
tests/test_browser.py::TestBrowserLifecycle::test_context_manager PASSED [ 50%]
tests/test_browser.py::TestBrowserLifecycle::test_stop_is_idempotent PASSED [ 53%]
tests/test_browser.py::TestNavigation::test_open_returns_title PASSED    [ 56%]
tests/test_browser.py::TestNavigation::test_url PASSED                   [ 59%]
tests/test_browser.py::TestNavigation::test_open_subpage PASSED          [ 62%]
tests/test_browser.py::TestPageInfo::test_html PASSED                    [ 65%]
tests/test_browser.py::TestPageInfo::test_text PASSED                    [ 68%]
tests/test_browser.py::TestInteraction::test_click PASSED                [ 71%]
tests/test_browser.py::TestInteraction::test_input PASSED                [ 75%]
tests/test_browser.py::TestInteraction::test_js PASSED                   [ 78%]
tests/test_browser.py::TestInteraction::test_js_string PASSED            [ 81%]
tests/test_browser.py::TestElementChecks::test_exists_true PASSED        [ 84%]
tests/test_browser.py::TestElementChecks::test_exists_false PASSED       [ 87%]
tests/test_browser.py::TestElementChecks::test_count PASSED              [ 90%]
tests/test_browser.py::TestElementChecks::test_visible_true PASSED       [ 93%]
tests/test_browser.py::TestElementChecks::test_visible_false PASSED      [ 96%]
tests/test_browser.py::TestErrors::test_error_raises_rodney_error PASSED [100%]

======================== 32 passed in 101.50s (0:01:41) ========================
```

## Source Code for Demo Scripts

```bash
cat /tmp/demo_basic.py
```

```output
import rodney

with rodney.Browser() as browser:
    title = browser.open("https://www.example.com")
    print(f"Title: {title}")
    print(f"URL:   {browser.url()}")
    print(f"Text:  {browser.text('h1')}")
```

```bash
cat /tmp/demo_js.py
```

```output
import rodney

with rodney.Browser() as browser:
    browser.open("https://www.example.com")

    print("Number:", browser.js("1 + 2"))
    print("String:", browser.js("document.title"))
    print("Boolean:", browser.js("document.title === 'Example Domain'"))
    print("Array:", browser.js("[1, 2, 3]"))
    print("Null:", browser.js("null"))
```

```bash
cat /tmp/demo_checks.py
```

```output
import rodney

with rodney.Browser() as browser:
    browser.open("https://www.example.com")

    print("h1 exists:", browser.exists("h1"))
    print("#nope exists:", browser.exists("#nope"))
    print("p count:", browser.count("p"))
    print("h1 visible:", browser.visible("h1"))
```

```bash
cat /tmp/demo_form.py
```

```output
import http.server, threading, rodney

# Spin up a tiny test server with a form
html = b"""<!DOCTYPE html><html><head><title>Form Demo</title></head><body>
<form id="f"><input id="name" type="text"><select id="color">
<option value="red">Red</option><option value="blue">Blue</option>
</select><button id="go" type="submit">Go</button></form></body></html>"""

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(html)
    def log_message(self, *a): pass

srv = http.server.HTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()
url = f"http://127.0.0.1:{srv.server_address[1]}"

with rodney.Browser() as browser:
    browser.open(url)
    print("Page:", browser.title())

    browser.input("#name", "Alice")
    val = browser.js('document.querySelector("#name").value')
    print("Name field:", val)

    browser.select("#color", "blue")
    val = browser.js('document.querySelector("#color").value')
    print("Color field:", val)

    browser.clear("#name")
    val = browser.js('document.querySelector("#name").value')
    print("After clear:", repr(val))

srv.shutdown()
```

```bash
cat /tmp/demo_errors.py
```

```output
import rodney

with rodney.Browser() as browser:
    browser.open("https://www.example.com")

    # exists() returns False instead of raising
    print("Missing element exists:", browser.exists("#nope"))

    # But click on a missing element raises RodneyError
    try:
        browser.click("#nope")
    except rodney.RodneyError as e:
        print(f"RodneyError: {e}")
```

```bash
cat /tmp/demo_html.py
```

```output
import rodney

with rodney.Browser() as browser:
    browser.open("https://www.example.com")
    html = browser.html("h1")
    print("Element HTML:", html.strip())
    print("Title attr:", browser.attr("a", "href"))
```

```bash
cat /tmp/demo_async_basic.py
```

```output
import asyncio
import rodney

async def main():
    async with rodney.AsyncBrowser() as browser:
        title = await browser.open("https://www.example.com")
        print(f"Title: {title}")
        print(f"URL:   {await browser.url()}")
        print(f"h1:    {await browser.text('h1')}")
        print(f"JS:    {await browser.js('2 ** 10')}")

asyncio.run(main())
```

```bash
cat /tmp/demo_concurrent.py
```

```output
import asyncio, time, rodney

async def fetch_title(url):
    async with rodney.AsyncBrowser() as browser:
        await browser.open(url)
        return await browser.title()

async def main():
    urls = [
        "https://www.example.com",
        "https://www.example.org",
        "https://www.example.net",
    ]
    start = time.monotonic()
    titles = await asyncio.gather(*(fetch_title(u) for u in urls))
    elapsed = time.monotonic() - start
    for url, title in zip(urls, titles):
        print(f"{url} -> {title}")
    print(f"\n3 browsers concurrently in {elapsed:.1f}s")

asyncio.run(main())
```

```bash
cat /tmp/demo_factory.py
```

```output
import asyncio
import rodney

async def main():
    browser = await rodney.AsyncBrowser.start()
    try:
        await browser.open("https://www.example.com")
        print("Title:", await browser.title())
        print("Running:", browser._started)
    finally:
        await browser.stop()
    print("After stop:", browser._started)

asyncio.run(main())
```

```bash
cat /tmp/demo_crash.py
```

```output
import subprocess, os, signal, time

# Start a Python script that opens a browser but gets killed
child = subprocess.Popen(
    ["python3", "-c", """
import rodney, os, time, sys
browser = rodney.Browser()
browser.open("https://www.example.com")
print(f"rodney_pid={browser._proc.pid}", flush=True)
time.sleep(60)  # hang around — will be killed
"""],
    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    cwd="/home/user/rodney/python",
    env={**os.environ, "VIRTUAL_ENV": "/home/user/rodney/python/.venv",
         "PATH": "/home/user/rodney/python/.venv/bin:" + os.environ["PATH"]},
)

# Read the rodney PID
line = child.stdout.readline().decode().strip()
rodney_pid = int(line.split("=")[1])
print(f"Child Python PID: {child.pid}")
print(f"Rodney serve PID: {rodney_pid}")

# Kill the Python process hard (SIGKILL — no cleanup handlers run)
os.kill(child.pid, signal.SIGKILL)
child.wait()
print("Python killed with SIGKILL")

# Give rodney a moment to notice stdin EOF and clean up
time.sleep(2)

# Check if rodney is still alive
try:
    os.kill(rodney_pid, 0)
    print(f"Rodney PID {rodney_pid}: STILL RUNNING (bad)")
except ProcessLookupError:
    print(f"Rodney PID {rodney_pid}: cleaned up (good)")
```
