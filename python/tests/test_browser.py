"""Sync Browser tests — red phase first, then green."""

import rodney


class TestBrowserLifecycle:
    def test_context_manager(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            assert browser.title() == "Test Page"

    def test_stop_is_idempotent(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            browser.stop()
            browser.stop()  # should not raise


class TestNavigation:
    def test_open_returns_title(self, test_server):
        with rodney.Browser() as browser:
            result = browser.open(test_server)
            assert result == "Test Page"

    def test_url(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            assert test_server in browser.url()

    def test_open_subpage(self, test_server):
        with rodney.Browser() as browser:
            browser.open(f"{test_server}/form")
            assert browser.title() == "Form Page"


class TestPageInfo:
    def test_html(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            html = browser.html("#greeting")
            assert "Welcome to the test page" in html

    def test_text(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            text = browser.text("#greeting")
            assert text == "Welcome to the test page"


class TestInteraction:
    def test_click(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            browser.click("#form-link")
            assert browser.title() == "Form Page"

    def test_input(self, test_server):
        with rodney.Browser() as browser:
            browser.open(f"{test_server}/form")
            browser.input("#name-input", "Alice")
            val = browser.js("document.querySelector('#name-input').value")
            assert val == "Alice"

    def test_js(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            result = browser.js("1 + 2")
            assert result == 3

    def test_js_string(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            result = browser.js("document.title")
            assert result == "Test Page"


class TestElementChecks:
    def test_exists_true(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            assert browser.exists("#greeting") is True

    def test_exists_false(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            assert browser.exists("#nonexistent") is False

    def test_count(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            assert browser.count("p") >= 1

    def test_visible_true(self, test_server):
        with rodney.Browser() as browser:
            browser.open(f"{test_server}/form")
            assert browser.visible("#name-input") is True

    def test_visible_false(self, test_server):
        with rodney.Browser() as browser:
            browser.open(f"{test_server}/form")
            assert browser.visible("#hidden-div") is False


class TestErrors:
    def test_error_raises_rodney_error(self, test_server):
        with rodney.Browser() as browser:
            browser.open(test_server)
            try:
                browser.click("#nonexistent-element-xyz")
                assert False, "Should have raised"
            except rodney.RodneyError:
                pass
