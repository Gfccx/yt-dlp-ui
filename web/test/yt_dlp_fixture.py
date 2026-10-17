#!/usr/bin/env python3
"""Fake yt-dlp for browser integration; never contacts public services."""
import json
import os
import pathlib
import sys

args = sys.argv[1:]
url = args[-1]
with open(os.environ["AUDIO_CAPTURE"], "a") as output:
    output.write(json.dumps(args) + "\n")
with open(os.environ["AUDIO_FIXTURE"]) as source:
    info = json.load(source)
info["webpage_url"] = url
info["title"] = "Audio fixture"
# Synthetic video stream used only for this test.
info["formats"].append({"format_id": "248", "vcodec": "vp9", "acodec": "none", "height": 1080, "ext": "webm", "protocol": "https"})
if "single" in url:
    info["formats"] = [f for f in info["formats"] if f.get("language") == "zh-Hant" or f["format_id"] == "248"]
if "english-only" in url:
    info["formats"] = [f for f in info["formats"] if f.get("language") == "en" or f["format_id"] == "248"]
    info["requested_formats"] = [info["formats"][1]]
if "-J" in args:
    if "--yes-playlist" in args:
        info = {"_type": "playlist", "id": "list", "title": "Language playlist", "entries": [
            {"id": "one", "title": "Chinese track", "url": "https://video.invalid/one"},
            {"id": "two", "title": "Missing Chinese", "url": "https://video.invalid/english-only"},
            {"id": "three", "title": "Retryable track", "url": "https://video.invalid/fail-once"},
        ]}
    print(json.dumps(info))
else:
    if "fail-once" in url:
        flag = pathlib.Path(os.environ["AUDIO_FAIL_ONCE"])
        if not flag.exists():
            flag.write_text("failed")
            print("ERROR: temporary media connection failure", file=sys.stderr)
            sys.exit(1)
    selector = args[args.index("-f") + 1]
    output = pathlib.Path(args[args.index("-P") + 1]) / ".tmp.Audio_[fixture].webm"
    output.write_text(selector)
    print('download:{"status":"downloading","_percent":50}')
    print("filepath=" + str(output))
    print("height=1080\nvcodec=vp9\nacodec=opus\nabr=128")
