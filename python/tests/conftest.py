import http.server
import subprocess
import sys
import threading

import pytest


HTML_PAGES = {
    "/": b"""<!DOCTYPE html>
<html><head><title>Test Page</title></head>
<body>
<h1>Hello</h1>
<p id="greeting">Welcome to the test page</p>
<a href="/form" id="form-link">Go to form</a>
</body></html>""",
    "/form": b"""<!DOCTYPE html>
<html><head><title>Form Page</title></head>
<body>
<h1>Form</h1>
<form id="myform" action="/submit" method="post">
  <input id="name-input" type="text" name="name" />
  <select id="color-select" name="color">
    <option value="red">Red</option>
    <option value="blue">Blue</option>
  </select>
  <button id="submit-btn" type="submit">Submit</button>
</form>
<div id="hidden-div" style="display:none">Secret</div>
</body></html>""",
    "/submit": b"""<!DOCTYPE html>
<html><head><title>Submitted</title></head>
<body><h1>Thanks!</h1></body></html>""",
}


class _TestHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = HTML_PAGES.get(self.path)
        if body is None:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        self.do_GET()

    def log_message(self, format, *args):
        pass  # suppress request logging


@pytest.fixture(scope="session")
def test_server():
    """Start a localhost HTTP server in a background thread.

    Returns the base URL, e.g. 'http://127.0.0.1:PORT'.
    """
    server = http.server.HTTPServer(("127.0.0.1", 0), _TestHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{port}"
    server.shutdown()
