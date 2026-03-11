"""Synchronous Browser that communicates with rodney serve over JSON-over-stdio."""

from __future__ import annotations

import json
import subprocess
import shutil
import tempfile
import os
from pathlib import Path

from rodney._types import RodneyError, CheckFailed, RunResult


def _parse_js(stdout: str):
    """Parse rodney js output into a Python value."""
    raw = stdout.strip()
    if raw in ("null", "undefined", ""):
        return None
    try:
        return json.loads(raw)
    except (json.JSONDecodeError, ValueError):
        return raw


class Browser:
    """Sync browser that drives rodney via the ``rodney serve`` JSON protocol."""

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
        self._proc: subprocess.Popen | None = None
        self._started = False
        self._start()

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

    def _start(self):
        self._proc = subprocess.Popen(
            self._serve_args(),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=self._env(),
        )
        # Wait for ready message
        line = self._proc.stdout.readline()
        if not line:
            err = self._proc.stderr.read().decode() if self._proc.stderr else ""
            raise RodneyError(f"rodney serve failed to start: {err}")
        ready = json.loads(line)
        if not ready.get("ok"):
            raise RodneyError("rodney serve sent non-ok ready message")
        self._started = True

    def _run(self, *args: str, check_exit_1: bool = False) -> RunResult:
        if not self._started or self._proc is None:
            raise RodneyError("Browser is not running")
        self._next_id += 1
        req = {"id": self._next_id, "cmd": args[0]}
        if len(args) > 1:
            req["args"] = list(args[1:])
        self._proc.stdin.write(json.dumps(req).encode() + b"\n")
        self._proc.stdin.flush()
        line = self._proc.stdout.readline()
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

    def stop(self):
        if not self._started:
            return
        self._started = False
        if self._proc and self._proc.stdin:
            try:
                self._proc.stdin.close()
            except OSError:
                pass
            try:
                self._proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self._proc.kill()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.stop()

    def __del__(self):
        self.stop()

    # --- Navigation ---

    def open(self, url: str) -> str:
        return self._run("open", url).stdout.strip()

    def back(self) -> str:
        return self._run("back").stdout.strip()

    def forward(self) -> str:
        return self._run("forward").stdout.strip()

    def reload(self, hard: bool = False) -> None:
        args = ["reload"]
        if hard:
            args.append("--hard")
        self._run(*args)

    def clear_cache(self) -> None:
        self._run("clear-cache")

    # --- Page info ---

    def url(self) -> str:
        return self._run("url").stdout.strip()

    def title(self) -> str:
        return self._run("title").stdout.strip()

    def html(self, selector: str | None = None) -> str:
        args = ["html"]
        if selector:
            args.append(selector)
        return self._run(*args).stdout

    def text(self, selector: str) -> str:
        return self._run("text", selector).stdout.strip()

    def attr(self, selector: str, attribute: str) -> str:
        return self._run("attr", selector, attribute).stdout.strip()

    # --- Interaction ---

    def js(self, expression: str):
        return _parse_js(self._run("js", expression).stdout)

    def click(self, selector: str) -> None:
        self._run("click", selector)

    def input(self, selector: str, text: str) -> None:
        self._run("input", selector, text)

    def clear(self, selector: str) -> None:
        self._run("clear", selector)

    def select(self, selector: str, value: str) -> str:
        return self._run("select", selector, value).stdout.strip()

    def submit(self, selector: str) -> None:
        self._run("submit", selector)

    def hover(self, selector: str) -> None:
        self._run("hover", selector)

    def focus(self, selector: str) -> None:
        self._run("focus", selector)

    # --- Waiting ---

    def wait(self, selector: str) -> None:
        self._run("wait", selector)

    def wait_load(self) -> None:
        self._run("waitload")

    def wait_stable(self) -> None:
        self._run("waitstable")

    def wait_idle(self) -> None:
        self._run("waitidle")

    # --- Element checks ---

    def exists(self, selector: str) -> bool:
        result = self._run("exists", selector)
        return result.returncode == 0

    def count(self, selector: str) -> int:
        return int(self._run("count", selector).stdout.strip())

    def visible(self, selector: str) -> bool:
        result = self._run("visible", selector)
        return result.returncode == 0
