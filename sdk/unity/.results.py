"""Prints one NUnit result file. Shared by the editor run and the device run."""

import os
import sys
import xml.etree.ElementTree as ET

path = os.environ["RESULTS"]
label = os.environ.get("LABEL", "")

if not os.path.exists(path):
    print("The run produced no results. See the log beside it.")
    raise SystemExit(1)

root = ET.parse(path).getroot()
failed = int(root.get("failed") or 0)
suffix = "  (%s)" % label if label else ""
print("%s passed, %s failed%s" % (root.get("passed"), root.get("failed"), suffix))

for case in root.iter("test-case"):
    print("  %-7s %s" % (case.get("result"), case.get("name")))
    if case.get("result") != "Passed":
        for message in case.iter("message"):
            print("          " + (message.text or "").strip()[:400])

raise SystemExit(1 if failed else 0)
