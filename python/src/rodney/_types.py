from dataclasses import dataclass


class RodneyError(Exception):
    """Raised when rodney returns exit_code 2 (real error)."""


class CheckFailed(Exception):
    """Raised when rodney returns exit_code 1 (check failed)."""


@dataclass
class RunResult:
    returncode: int
    stdout: str
    stderr: str
