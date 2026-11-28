"""Partition the compiled API test inventory and audit each selected family."""
import argparse
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
API = "borealis/api-backend/cmd/api-backend"
API_SHARDS = 4
LANES = ("static", "packages", *(f"api{i}" for i in range(1, API_SHARDS + 1)))


def select(listing, weights, lane):
    names = [line.strip() for line in listing.splitlines() if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line.strip())]
    if not names or len(names) != len(set(names)):
        raise ValueError("empty or duplicate compiled API test inventory")
    buckets = [[] for _ in range(API_SHARDS)]
    seconds = [0.0] * API_SHARDS
    for name in sorted(names, key=lambda n: (-weights.get(n, 0.25), n)):
        index = min(range(API_SHARDS), key=lambda i: (seconds[i], i))
        buckets[index].append(name)
        seconds[index] += weights.get(name, 0.25)
    return sorted(buckets[lane - 1])


def audit(names, stream, database):
    expected = set(names)
    started, settled, skipped = set(), set(), set()
    complete = False
    for line in stream:
        event = json.loads(line)
        action, name = event["Action"], event.get("Test")
        if event.get("Package") != API or complete or action in ("fail", "build-fail"):
            raise ValueError("unexpected, failed or extra API result")
        if not name:
            complete = action == "pass"
            continue
        root = name.split("/", 1)[0]
        if root not in expected:
            raise ValueError("unexpected API test " + name)
        if action == "run":
            if name in started:
                raise ValueError("duplicate API test " + name)
            started.add(name)
        if action in ("pass", "skip"):
            if name not in started or name in settled:
                raise ValueError("unexpected API test completion " + name)
            if action == "skip":
                if root not in database:
                    raise ValueError("unexpected API test skip " + name)
                skipped.add(name)
            settled.add(name)
    if not complete or started != settled or not expected.issubset(settled):
        raise ValueError("incomplete API shard")
    print(f"Audited {len(expected)} test families, {len(settled)} total results, {len(skipped)} database-dependent skips")


def receipt(identity, lane):
    return hashlib.sha256(json.dumps([*identity, lane], separators=(",", ":")).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listing", type=Path, required=True)
    parser.add_argument("--shard", type=int, choices=range(1, API_SHARDS + 1), required=True)
    parser.add_argument("--results", type=Path)
    args = parser.parse_args()
    weights = json.loads((ROOT / "Tests/manifests/engine-api-timings.json").read_bytes())
    tests = select(args.listing.read_text(), weights, args.shard)
    if args.results:
        manifest = json.loads((ROOT / "Tests/manifests/postgres-tests.json").read_bytes())
        with args.results.open() as stream:
            audit(tests, stream, {t for group in manifest["shards"] for t in group})
    else:
        print("^(" + "|".join(tests) + ")$")


if __name__ == "__main__":
    main()
