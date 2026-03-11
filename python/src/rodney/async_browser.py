"""Async Browser that communicates with rodney serve over JSON-over-stdio."""

from __future__ import annotations

import asyncio
import json
import os

from rodney._types import RodneyError, CheckFailed, RunResult
from rodney.browser import _parse_js


class AsyncBrowser:
    """Async browser that drives rodney via the ``rodney serve`` JSON protocol."""

    def __init__(
        self,
        *,
        bin: str = "rodney",
        headless: bool = True,
        insecure: bool = False,
        timeout: float = 30.0,
        chrome_bin: str | None = None,
    ):
        self._bin = bin
        self._headless = headless
        self._insecure = insecure
        self._timeout = timeout
        self._chrome_bin = chrome_bin
        self._next_id = 0
        self._proc: asyncio.subprocess.Process | None = None
        self._started = False

    def _env(self) -> dict[str, str]:
        env = os.environ.copy()
        env["ROD_TIMEOUT"] = str(self._timeout)
        if self._chrome_bin:
            env["ROD_CHROME_BIN"] = self._chrome_bin
        return env

    def _serve_args(self) -> list[str]:
        args = [self._bin, "serve"]
        if not self._headless:
            args.append("--show")
        if self._insecure:
            args.append("--insecure")
        return args

    async def _start(self):
        cmd = self._serve_args()
        self._proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=self._env(),
        )
        line = await self._proc.stdout.readline()
        if not line:
            err = ""
            if self._proc.stderr:
                err = (await self._proc.stderr.read()).decode()
            raise RodneyError(f"rodney serve failed to start: {err}")
        ready = json.loads(line)
        if not ready.get("ok"):
            raise RodneyError("rodney serve sent non-ok ready message")
        self._started = True

    async def _run(self, *args: str, check_exit_1: bool = False) -> RunResult:
        if not self._started or self._proc is None:
            raise RodneyError("Browser is not running")
        self._next_id += 1
        req = {"id": self._next_id, "cmd": args[0]}
        if len(args) > 1:
            req["args"] = list(args[1:])
        self._proc.stdin.write(json.dumps(req).encode() + b"\n")
        await self._proc.stdin.drain()
        line = await self._proc.stdout.readline()
        if not line:
            raise RodneyError("rodney serve closed unexpectedly")
        resp = json.loads(line)
        returncode = 0 if resp.get("ok") else resp.get("exit_code", 2)
        result = RunResult(
            returncode=returncode,
            stdout=resp.get("stdout", ""),
            stderr=resp.get("stderr", ""),
        )
        if result.returncode == 2:
            raise RodneyError(result.stderr.strip() or result.stdout.strip())
        if result.returncode == 1 and check_exit_1:
            raise CheckFailed(result.stdout.strip())
        return result

    async def stop(self):
        if not self._started:
            return
        self._started = False
        if self._proc and self._proc.stdin:
            try:
                self._proc.stdin.close()
            except OSError:
                pass
            try:
                await asyncio.wait_for(self._proc.wait(), timeout=10)
            except asyncio.TimeoutError:
                self._proc.kill()

    async def __aenter__(self):
        await self._start()
        return self

    async def __aexit__(self, *exc):
        await self.stop()

    @classmethod
    async def start(cls, **kwargs) -> AsyncBrowser:
        browser = cls(**kwargs)
        await browser._start()
        return browser

    # --- Navigation ---

    async def open(self, url: str) -> str:
        return (await self._run("open", url)).stdout.strip()

    async def back(self) -> str:
        return (await self._run("back")).stdout.strip()

    async def forward(self) -> str:
        return (await self._run("forward")).stdout.strip()

    async def reload(self, hard: bool = False) -> None:
        args = ["reload"]
        if hard:
            args.append("--hard")
        await self._run(*args)

    async def clear_cache(self) -> None:
        await self._run("clear-cache")

    # --- Page info ---

    async def url(self) -> str:
        return (await self._run("url")).stdout.strip()

    async def title(self) -> str:
        return (await self._run("title")).stdout.strip()

    async def html(self, selector: str | None = None) -> str:
        args = ["html"]
        if selector:
            args.append(selector)
        return (await self._run(*args)).stdout

    async def text(self, selector: str) -> str:
        return (await self._run("text", selector)).stdout.strip()

    async def attr(self, selector: str, attribute: str) -> str:
        return (await self._run("attr", selector, attribute)).stdout.strip()

    # --- Interaction ---

    async def js(self, expression: str):
        return _parse_js((await self._run("js", expression)).stdout)

    async def click(self, selector: str) -> None:
        await self._run("click", selector)

    async def input(self, selector: str, text: str) -> None:
        await self._run("input", selector, text)

    async def clear(self, selector: str) -> None:
        await self._run("clear", selector)

    async def select(self, selector: str, value: str) -> str:
        return (await self._run("select", selector, value)).stdout.strip()

    async def submit(self, selector: str) -> None:
        await self._run("submit", selector)

    async def hover(self, selector: str) -> None:
        await self._run("hover", selector)

    async def focus(self, selector: str) -> None:
        await self._run("focus", selector)

    # --- Waiting ---

    async def wait(self, selector: str) -> None:
        await self._run("wait", selector)

    async def wait_load(self) -> None:
        await self._run("waitload")

    async def wait_stable(self) -> None:
        await self._run("waitstable")

    async def wait_idle(self) -> None:
        await self._run("waitidle")

    # --- Element checks ---

    async def exists(self, selector: str) -> bool:
        result = await self._run("exists", selector)
        return result.returncode == 0

    async def count(self, selector: str) -> int:
        return int((await self._run("count", selector)).stdout.strip())

    async def visible(self, selector: str) -> bool:
        result = await self._run("visible", selector)
        return result.returncode == 0
