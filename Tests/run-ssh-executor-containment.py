#!/usr/bin/env python3
"""Opt-in Tier3 fixtures. Run only on an operator-approved development host."""

import datetime
import fcntl
import json
import os
import pathlib
import secrets
import subprocess
import time

REPO = pathlib.Path(__file__).resolve().parents[1]


def k3s_status():
    return subprocess.check_output(
        ["systemctl", "show", "k3s", "--property=ActiveState,NRestarts,ExecMainStartTimestamp"],
        text=True,
        timeout=5,
    )


def run_fixture(binary, directory, mode):
    directory.mkdir(mode=0o700)
    nonce = secrets.token_hex(32)
    environment = dict(os.environ, BOREALIS_EXECUTOR_FIXTURE="arguments",
                       BOREALIS_EXECUTOR_BINARY=str(binary), BOREALIS_EXECUTOR_NONCE=nonce)
    arguments = json.loads(subprocess.check_output(
        [str(binary), "-test.run=^TestExecutorServiceFixture$"], env=environment, text=True, timeout=5))
    assert arguments[-4:] == [str(binary), "bootstrap-session-contained", "--nonce", nonce]
    # Use production supervision properties, replacing only the command with
    # an unprivileged test process and passing public fixture metadata.
    separator = arguments.index("--")
    arguments = arguments[:separator] + [
        "--uid=" + str(os.getuid()), "--setenv=BOREALIS_EXECUTOR_FIXTURE=" + mode,
        "--setenv=BOREALIS_EXECUTOR_FIXTURE_DIR=" + str(directory),
        "--setenv=BOREALIS_EXECUTOR_NONCE=" + nonce,
        "--", str(binary), "-test.run=^TestExecutorServiceFixture$",
    ]
    unit = "borealis-bootstrap-" + nonce + ".service"
    (directory / "launch-arguments.json").write_text(json.dumps(arguments, indent=2) + "\n")
    start = time.monotonic()
    probe_environment = dict(os.environ, BOREALIS_EXECUTOR_FIXTURE="probe",
                             BOREALIS_EXECUTOR_FIXTURE_DIR=str(directory))

    def probe():
        return json.loads(subprocess.check_output(
            [str(binary), "-test.run=^TestExecutorServiceFixture$"],
            env=probe_environment, text=True, timeout=7))["quiescent"]

    with (directory / "unit.log").open("w") as log:
        process = subprocess.Popen(["sudo", "-n", "/usr/bin/systemd-run", *arguments],
                                   stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        try:
            started = directory / "started.json"
            while not started.exists() and process.poll() is None:
                if time.monotonic() - start > 8:
                    raise RuntimeError(f"Fixture did not become observable; inspect {directory}")
                time.sleep(0.02)
            if not started.exists():
                raise RuntimeError(f"Fixture failed before start; inspect {directory}")
            # Releasing the private mutation lock cannot establish quiescence.
            with (directory / "journal" / "mutation.lock").open("r+") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            active_rejected = not probe()
            (directory / "observer-release").touch(mode=0o600)
            code = process.wait(timeout=20)
            elapsed = time.monotonic() - start
            stopped_verified = probe()
            record = json.loads(started.read_text())
            stat = pathlib.Path("/proc") / str(record["descendant_pid"]) / "stat"
            try:
                state = stat.read_text().rsplit(")", 1)[1].split()[0]
            except FileNotFoundError:
                state = "absent"
            group = pathlib.Path("/sys/fs/cgroup") / record["identity"]["control_group"].lstrip("/")
            try:
                events = (group / "cgroup.events").read_text()
            except FileNotFoundError:
                events = "removed"
            heartbeat = directory / "descendant-heartbeat"
            snapshot = heartbeat.read_bytes()
            time.sleep(0.25)
            no_writes = heartbeat.read_bytes() == snapshot
            with (directory / "fixture.lock").open("r+") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            passed = (code == 0 if mode == "normal" else code != 0)
            passed = passed and state in ("absent", "Z") and no_writes and elapsed < 10
            passed = passed and active_rejected and stopped_verified
            passed = passed and (events == "removed" or "populated 0\n" in events)
            result = {"mode": mode, "unit": unit, "exit": code, "elapsed_seconds": round(elapsed, 3),
                      "descendant_state": state, "cgroup_events": events, "no_late_writes": no_writes,
                      "lock_released": True, "passed": passed, "evidence": str(directory)}
            result.update(active_executor_rejected_with_free_journal_lock=active_rejected,
                          stopped_executor_independently_verified=stopped_verified)
            (directory / "result.json").write_text(json.dumps(result, indent=2) + "\n")
            print(mode, "PASS" if passed else "FAIL", round(elapsed, 2), "seconds", flush=True)
            if not passed:
                raise RuntimeError(f"Containment failed; inspect {directory}")
            return result
        finally:
            # Proof above precedes cleanup. An interrupted/failed runner stops
            # only its own unpredictable fixture unit, never another service.
            try:
                cleanup = subprocess.run(["sudo", "-n", "systemctl", "stop", unit],
                                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                         text=True, timeout=8, check=False)
                (directory / "cleanup.log").write_text(cleanup.stdout)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=5)


def main():
    if os.geteuid() == 0:
        raise SystemExit("Run as development user; only disposable service management uses sudo.")
    go = os.environ.get("BOREALIS_GO_BIN", "go")
    if subprocess.check_output([go, "env", "GOVERSION"], text=True, timeout=10).strip() != "go1.25.12":
        raise SystemExit("Go1.25.12 required for executor fixtures.")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    root = REPO / "Unit_Test_Results" / ("s01-executor-containment-" + stamp)
    root.mkdir(mode=0o700, parents=True)
    binary = root / "executor-fixture.test"
    subprocess.run([go, "test", "-c", "-o", str(binary), "./internal/clusterbootstrap"],
                   cwd=REPO / "Data/Engine/Containers/api-backend", check=True, timeout=120)
    before = k3s_status()
    results = []
    try:
        for mode in ("normal", "orphan-timeout", "crash", "watchdog", "lease-expiry"):
            results.append(run_fixture(binary, root / mode, mode))
    finally:
        after = k3s_status()
        (root / "results.json").write_text(json.dumps(
            {"results": results, "k3s_before": before, "k3s_after": after}, indent=2) + "\n")
    if before != after:
        raise RuntimeError("K3s baseline changed during fixture run; retain evidence and investigate.")
    print("All fixture services stopped; K3s baseline unchanged. Results:", root, flush=True)


if __name__ == "__main__":
    main()
