#!/usr/bin/env python3
"""Apply panel-owned API credentials without giving the project Docker access."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import threading
import time


def read_configuration(filename):
    try:
        with open(filename, encoding="utf-8") as source:
            document = json.load(source)
        revision, config = document["revision"], document["config"]
        if not isinstance(revision, str) or not re.fullmatch(r"[a-f0-9]{64}", revision):
            return None
        if config.get("enabled") is False:
            return revision, None
        api_id, api_hash = config.get("apiId"), config.get("apiHash")
        if config.get("enabled") is not True or type(api_id) is not int or not 0 < api_id <= 2147483647:
            return None
        if not isinstance(api_hash, str) or not re.fullmatch(r"[a-fA-F0-9]{32}", api_hash):
            return None
        return revision, (str(api_id), api_hash)
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return None


def write_status(filename, revision, state):
    filename = Path(filename)
    data = {"revision": revision, "state": state, "updatedAt": int(time.time())}
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=filename.parent, prefix=".panel-status-", delete=False) as output:
            temporary = output.name
            os.fchmod(output.fileno(), 0o644)
            json.dump(data, output)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, filename)
    finally:
        if temporary and os.path.exists(temporary):
            os.unlink(temporary)


def stop_process(process):
    if process is None:
        return
    if process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    process.wait()


def supervise(config_path, status_path, command, stop, interval=1, retry_delay=5):
    process, applied, retry_at = None, None, 0
    previous_state = None
    try:
        while not stop.is_set():
            desired = read_configuration(config_path)
            if desired != applied:
                stop_process(process)
                process, applied, retry_at = None, desired, 0
            revision, credentials = desired if desired else ("", None)
            state = "waiting_config" if desired is None else "disabled"
            if credentials is not None:
                if process is not None and process.poll() is not None:
                    process.wait()
                    process, retry_at = None, time.monotonic() + retry_delay
                if process is None and time.monotonic() >= retry_at:
                    environment = os.environ.copy()
                    environment.pop("VIDEO_TELEGRAM_BOT_TOKEN", None)
                    environment["TELEGRAM_API_ID"], environment["TELEGRAM_API_HASH"] = credentials
                    try:
                        # Credentials are environment values, never command arguments.
                        process = subprocess.Popen(command, env=environment, start_new_session=True,
                                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                    except OSError:
                        retry_at = time.monotonic() + retry_delay
                state = "running" if process is not None else "retrying"
            try:
                write_status(status_path, revision, state)
            except OSError:
                # Do not run an unobservable process with stale status.
                stop_process(process)
                process, retry_at, state = None, time.monotonic() + retry_delay, "status_error"
            if state != previous_state:
                print(f"[telegram-supervisor] {state}", flush=True)
                previous_state = state
            stop.wait(interval)
    finally:
        stop_process(process)
        try:
            write_status(status_path, "", "stopped")
        except OSError:
            pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="/run/video-site-telegram/config.json")
    parser.add_argument("--directory", default="/var/lib/telegram-bot-api")
    parser.add_argument("--binary", default="/usr/local/bin/telegram-bot-api")
    parser.add_argument("--http-port", type=int, default=7878)
    args = parser.parse_args()
    if not 1 <= args.http_port <= 65535:
        parser.error("--http-port must be between 1 and 65535")
    stop = threading.Event()
    for signum in (signal.SIGTERM, signal.SIGINT):
        signal.signal(signum, lambda *_: stop.set())
    supervise(args.config, Path(args.directory) / "panel-status.json",
              [args.binary, "--local", f"--dir={args.directory}", f"--http-port={args.http_port}"], stop)


if __name__ == "__main__":
    main()
