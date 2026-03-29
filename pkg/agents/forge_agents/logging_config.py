"""
Structured JSON logging for Forge agents.

Call configure_logging() once at process startup (in dispatcher/runner main()).
Use get_task_logger() to get a logger with task_id and agent_role pre-attached.

Output format (one JSON object per line):
  {"time": "...", "level": "INFO", "service": "...", "task_id": "...", "agent_role": "...", "message": "..."}
"""
import logging
import os
import json
from datetime import datetime, timezone
from typing import Optional


class _JSONFormatter(logging.Formatter):
    """Formats log records as single-line JSON objects."""

    def __init__(self, service: str):
        super().__init__()
        self.service = service

    def format(self, record: logging.LogRecord) -> str:
        entry = {
            "time": datetime.fromtimestamp(record.created, tz=timezone.utc).isoformat(),
            "level": record.levelname,
            "service": self.service,
            "logger": record.name,
            "message": record.getMessage(),
        }
        # Propagate extra fields added via LoggerAdapter or logger.xxx(extra={...})
        for key, value in record.__dict__.items():
            if key not in (
                "args", "created", "exc_info", "exc_text", "filename",
                "funcName", "levelname", "levelno", "lineno", "message",
                "module", "msecs", "msg", "name", "pathname", "process",
                "processName", "relativeCreated", "stack_info", "thread",
                "threadName",
            ):
                entry[key] = value

        if record.exc_info:
            entry["exc_info"] = self.formatException(record.exc_info)

        return json.dumps(entry, default=str)


def configure_logging(service: str) -> None:
    """Configure the root logger with JSON output.

    Should be called once in each process's main() before any other logging.
    Subsequent calls in imported modules are ignored because the root logger
    handlers are already set.
    """
    level_name = os.environ.get("LOG_LEVEL", "INFO").upper()
    level = getattr(logging, level_name, logging.INFO)

    root = logging.getLogger()
    # Avoid adding duplicate handlers if called more than once.
    if root.handlers:
        return

    handler = logging.StreamHandler()
    handler.setFormatter(_JSONFormatter(service))
    root.setLevel(level)
    root.addHandler(handler)


def get_task_logger(base_logger: logging.Logger, task_id: str, agent_role: str) -> logging.LoggerAdapter:
    """Return a LoggerAdapter that appends task_id and agent_role to every record."""
    return logging.LoggerAdapter(base_logger, {"task_id": task_id, "agent_role": agent_role})
