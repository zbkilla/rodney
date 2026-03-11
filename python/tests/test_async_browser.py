"""Async Browser tests — red phase first, then green."""

import pytest

import rodney


class TestAsyncBrowserLifecycle:
    async def test_async_context_manager(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            assert await browser.title() == "Test Page"

    async def test_stop_is_idempotent(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            await browser.stop()
            await browser.stop()


class TestAsyncNavigation:
    async def test_open_returns_title(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            result = await browser.open(test_server)
            assert result == "Test Page"

    async def test_url(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            assert test_server in await browser.url()


class TestAsyncPageInfo:
    async def test_html(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            html = await browser.html("#greeting")
            assert "Welcome to the test page" in html

    async def test_text(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            text = await browser.text("#greeting")
            assert text == "Welcome to the test page"


class TestAsyncInteraction:
    async def test_click(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            await browser.click("#form-link")
            assert await browser.title() == "Form Page"

    async def test_input(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(f"{test_server}/form")
            await browser.input("#name-input", "Alice")
            val = await browser.js("document.querySelector('#name-input').value")
            assert val == "Alice"

    async def test_js(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            result = await browser.js("1 + 2")
            assert result == 3


class TestAsyncElementChecks:
    async def test_exists_true(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            assert await browser.exists("#greeting") is True

    async def test_exists_false(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            assert await browser.exists("#nonexistent") is False

    async def test_count(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            assert await browser.count("p") >= 1

    async def test_visible_true(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(f"{test_server}/form")
            assert await browser.visible("#name-input") is True

    async def test_visible_false(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(f"{test_server}/form")
            assert await browser.visible("#hidden-div") is False


class TestAsyncErrors:
    async def test_error_raises_rodney_error(self, test_server):
        async with rodney.AsyncBrowser() as browser:
            await browser.open(test_server)
            with pytest.raises(rodney.RodneyError):
                await browser.click("#nonexistent-element-xyz")
