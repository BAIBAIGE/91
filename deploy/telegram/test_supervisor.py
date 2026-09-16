import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("supervisor", Path(__file__).with_name("supervisor.py"))
supervisor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(supervisor)


class SupervisorTest(unittest.TestCase):
    def test_http_port_is_passed_to_child(self):
        for arguments, expected in [([], "7878"), (["--http-port", "8787"], "8787")]:
            with self.subTest(port=expected), mock.patch.object(sys, "argv", ["supervisor.py", *arguments]), \
                    mock.patch.object(supervisor, "supervise") as run, \
                    mock.patch.object(supervisor.signal, "signal"):
                supervisor.main()
                self.assertIn("--http-port=" + expected, run.call_args.args[2])

    def test_invalid_http_port_is_rejected(self):
        for port in ["0", "65536", "invalid"]:
            with self.subTest(port=port), mock.patch.object(sys, "argv", ["supervisor.py", "--http-port", port]), \
                    mock.patch.object(sys, "stderr"), mock.patch.object(supervisor, "supervise") as run:
                with self.assertRaises(SystemExit) as result:
                    supervisor.main()
                self.assertEqual(result.exception.code, 2)
                run.assert_not_called()

    def test_wait_reload_disable_and_stop(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, status, events = root / "config.json", root / "status.json", root / "events"
            # Use real child processes to verify exit-before-restart and env delivery.
            child = root / "child.py"
            child.write_text('''import os, signal, sys, time
from pathlib import Path
p = Path(sys.argv[1])
def record(text):
    with p.open("a") as f: f.write(text + "\\n")
def stop(*_):
    record("stop:" + os.environ["TELEGRAM_API_ID"])
    sys.exit(0)
signal.signal(signal.SIGTERM, stop)
assert "VIDEO_TELEGRAM_BOT_TOKEN" not in os.environ
record("start:" + os.environ["TELEGRAM_API_ID"] + ":" + os.environ["TELEGRAM_API_HASH"])
while True: time.sleep(.01)
''')
            stop = threading.Event()
            worker = threading.Thread(target=supervisor.supervise,
                args=(config, status, [sys.executable, str(child), str(events)], stop),
                kwargs={"interval": .02, "retry_delay": .05})
            worker.start()
            def wait_for(predicate):
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    if predicate(): return
                    time.sleep(.01)
                self.fail("supervisor did not reach expected state")
            def write(revision, enabled, api_id=1234):
                temporary = root / "next.json"
                temporary.write_text(json.dumps({"revision": revision * 64,
                    "config": {"enabled": enabled, "apiId": api_id, "apiHash": "a" * 32}}))
                os.replace(temporary, config)
            def lines():
                return events.read_text().splitlines() if events.exists() else []
            try:
                wait_for(status.exists)
                self.assertEqual(json.loads(status.read_text())["state"], "waiting_config")
                self.assertFalse(events.exists())
                write("a", True)
                wait_for(lambda: len(lines()) == 1)
                time.sleep(.1)
                self.assertEqual(len(lines()), 1)
                write("b", True, 5678)
                wait_for(lambda: len(lines()) == 3)
                self.assertEqual(lines()[1], "stop:1234")
                self.assertEqual(lines()[2], "start:5678:" + "a" * 32)
                self.assertNotIn("apiHash", status.read_text())
                write("c", False)
                wait_for(lambda: len(lines()) == 4)
                self.assertEqual(lines()[3], "stop:5678")
                config.write_text("invalid JSON")
                wait_for(lambda: json.loads(status.read_text())["state"] == "waiting_config")
                write("d", True)
                wait_for(lambda: len(lines()) == 5)
            finally:
                stop.set()
                worker.join(timeout=3)
                self.assertFalse(worker.is_alive())
            self.assertEqual(lines()[-1], "stop:1234")
            self.assertEqual(json.loads(status.read_text())["state"], "stopped")

    def test_invalid_credentials_are_not_executed(self):
        with tempfile.TemporaryDirectory() as directory:
            filename = Path(directory) / "config.json"
            for config in [{"enabled": True, "apiId": True, "apiHash": "a" * 32},
                           {"enabled": True, "apiId": -1, "apiHash": "a" * 32},
                           {"enabled": True, "apiId": 12, "apiHash": "$(touch injected)"}]:
                filename.write_text(json.dumps({"revision": "a" * 64, "config": config}))
                self.assertIsNone(supervisor.read_configuration(filename))

    def test_child_failure_retries(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, status, events = root / "config.json", root / "status.json", root / "events"
            config.write_text(json.dumps({"revision": "a" * 64, "config":
                {"enabled": True, "apiId": 12, "apiHash": "a" * 32}}))
            command = [sys.executable, "-c", "import sys; open(sys.argv[1], 'a').write('attempt\\n'); sys.exit(1)", str(events)]
            stop = threading.Event()
            worker = threading.Thread(target=supervisor.supervise,args=(config,status,command,stop),
                                      kwargs={"interval": .02,"retry_delay": .05})
            worker.start()
            try:
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    if events.exists() and len(events.read_text().splitlines()) >= 2: break
                    time.sleep(.02)
                self.assertGreaterEqual(len(events.read_text().splitlines()), 2)
            finally:
                stop.set()
                worker.join(timeout=3)
                self.assertFalse(worker.is_alive())


if __name__ == "__main__":
    unittest.main()
