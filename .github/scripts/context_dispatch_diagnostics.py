#!/usr/bin/env python3
"""Prepare temporary context-checker overlays for the focused CI comparison.

Both binaries record all goroutine stacks if a scenario takes two seconds.
The comparison changes only filtered Resume calls; it does not modify either
repository or relax the workflow task deadline. Never use it in a worker build.
"""

import argparse
import json
from pathlib import Path


def replace_once(source, old, new):
    if source.count(old) != 1:
        raise ValueError("diagnostic overlay no longer matches the SDK source")
    return source.replace(old, new, 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("sdk", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    sdk, output = args.sdk.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)

    checker = sdk / "example/contextpropagation/check/main.go"
    watched = replace_once(
        checker.read_text(),
        "func run(scenario string) {",
        '''func run(scenario string) {
    watchdog := time.AfterFunc(2*time.Second, func() {
        buf := make([]byte, 1<<20)
        n := runtime.Stack(buf, true)
        fmt.Printf("DISPATCH WATCHDOG scenario=%s GOMAXPROCS=%d\\n%s\\n", scenario, runtime.GOMAXPROCS(0), buf[:n])
    })
    defer watchdog.Stop()''',
    )
    watched_path = output / "watched-checker.go"
    watched_path.write_text(watched)
    replacements = {str(checker): str(watched_path)}
    (output / "current.json").write_text(json.dumps({"Replace": replacements}))

    bridge = sdk / "temporalbridge/bridge.go"
    unfiltered = replace_once(
        bridge.read_text(),
        '''		if phase == inboundDispatch {
			resume = instance.ResumeInbound
		} else if phase == priorityDispatch {
			resume = instance.ResumePriority
		}
''',
        "",
    )
    unfiltered_path = output / "unfiltered-bridge.go"
    unfiltered_path.write_text(unfiltered)
    replacements[str(bridge)] = str(unfiltered_path)
    (output / "unfiltered.json").write_text(json.dumps({"Replace": replacements}))


if __name__ == "__main__":
    main()
