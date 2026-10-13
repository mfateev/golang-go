#!/usr/bin/env python3
"""Prepare temporary context-checker overlays for the focused CI comparison.

The SDK records the focused task scope before timeout cleanup. This comparison
changes only filtered Resume calls; it does not modify either repository or
relax the workflow task deadline. Never use it in a worker build.
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

    replacements = {}
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
