#!/usr/bin/env python3
"""
devcontainer_cache_report.py

Parses a GitHub Actions log for the devcontainer build step and summarizes how
BuildKit and the registry cache were used. The script is intentionally small so
it can be copied into future investigations without extra dependencies.

Example:
    gh run view 23728700999 --log > /tmp/run.log
    python tools/devcontainer_cache_report.py --log /tmp/run.log \
        --job build-devcontainer --markdown
"""

from __future__ import annotations

import argparse
import json
import re
from collections import OrderedDict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, List, Optional, Sequence


STEP_LINE_RE = re.compile(r"#(?P<num>\d+)\s+\[(?P<stage>[^\]]+)\]\s+(?P<command>.+)")
STEP_CACHED_RE = re.compile(r"#(?P<num>\d+)\s+CACHED\b")
STEP_DONE_RE = re.compile(r"#(?P<num>\d+)\s+DONE\s+(?P<duration>[\d\.]+)(?P<unit>[a-z]+)", re.IGNORECASE)
LAYER_EXISTS_RE = re.compile(r"(?P<digest>[0-9a-f]{12}):\s+Layer already exists")
LAYER_PUSHED_RE = re.compile(r"(?P<digest>[0-9a-f]{12}):\s+Pushed")


@dataclass
class StepInfo:
    num: int
    stage: Optional[str] = None
    command: Optional[str] = None
    cached: bool = False
    duration: Optional[float] = None
    duration_unit: Optional[str] = None

    def label(self) -> str:
        if self.command:
            # Trim excessive whitespace to keep tables readable.
            cmd = re.sub(r"\s+", " ", self.command.strip())
            return cmd
        return self.stage or ""


@dataclass
class CacheReport:
    job: str
    cached_steps: List[StepInfo] = field(default_factory=list)
    executed_steps: List[StepInfo] = field(default_factory=list)
    existing_layers: List[str] = field(default_factory=list)
    pushed_layers: List[str] = field(default_factory=list)

    def to_dict(self) -> dict:
        def step_to_dict(step: StepInfo) -> dict:
            return OrderedDict(
                [
                    ("step", step.num),
                    ("cached", step.cached),
                    ("stage", step.stage),
                    ("command", step.command),
                    ("duration", step.duration),
                    ("duration_unit", step.duration_unit),
                ]
            )

        return OrderedDict(
            [
                ("job", self.job),
                ("cached_steps", [step_to_dict(s) for s in self.cached_steps]),
                ("executed_steps", [step_to_dict(s) for s in self.executed_steps]),
                ("existing_layers", self.existing_layers),
                ("pushed_layers", self.pushed_layers),
            ]
        )

    def to_markdown(self) -> str:
        lines = [
            f"### Job `{self.job}` cache summary",
            "",
            f"- BuildKit cache hits: {len(self.cached_steps)} steps ({', '.join(f'#{s.num}' for s in self.cached_steps) or 'none'})",
            f"- BuildKit executed steps: {len(self.executed_steps)} ({', '.join(f'#{s.num}' for s in self.executed_steps) or 'none'})",
            f"- Registry layers reused: {len(self.existing_layers)}",
            f"- Registry layers pushed: {len(self.pushed_layers)}",
            "",
        ]

        if self.cached_steps:
            lines.append("| Step | Stage / Command |")
            lines.append("|------|-----------------|")
            for step in self.cached_steps:
                lines.append(f"| #{step.num} | {step.label()} |")
            lines.append("")

        if self.executed_steps:
            lines.append("| Step | Stage / Command | Duration |")
            lines.append("|------|-----------------|----------|")
            for step in self.executed_steps:
                duration = (
                    f"{step.duration:g}{step.duration_unit or ''}"
                    if step.duration is not None
                    else ""
                )
                lines.append(f"| #{step.num} | {step.label()} | {duration} |")
            lines.append("")

        if self.existing_layers:
            lines.append(f"Reused layers ({len(self.existing_layers)}): `{', '.join(self.existing_layers)}`")
            lines.append("")
        if self.pushed_layers:
            lines.append(f"Pushed layers ({len(self.pushed_layers)}): `{', '.join(self.pushed_layers)}`")
            lines.append("")
        return "\n".join(lines)


def parse_lines(lines: Iterable[str], job: str) -> CacheReport:
    steps: dict[int, StepInfo] = {}
    existing_layers: List[str] = []
    pushed_layers: List[str] = []

    for raw_line in lines:
        if not raw_line.startswith(job + "\t"):
            continue

        line = raw_line.split("\t", 2)[-1].strip()

        match = STEP_LINE_RE.search(line)
        if match:
            num = int(match.group("num"))
            info = steps.setdefault(num, StepInfo(num=num))
            info.stage = match.group("stage")
            info.command = match.group("command")
            continue

        match = STEP_CACHED_RE.search(line)
        if match:
            num = int(match.group("num"))
            info = steps.setdefault(num, StepInfo(num=num))
            info.cached = True
            continue

        match = STEP_DONE_RE.search(line)
        if match:
            num = int(match.group("num"))
            info = steps.setdefault(num, StepInfo(num=num))
            try:
                info.duration = float(match.group("duration"))
            except ValueError:
                info.duration = None
            info.duration_unit = match.group("unit")
            continue

        match = LAYER_EXISTS_RE.search(line)
        if match:
            digest = match.group("digest")
            if digest not in existing_layers:
                existing_layers.append(digest)
            continue

        match = LAYER_PUSHED_RE.search(line)
        if match:
            digest = match.group("digest")
            if digest not in pushed_layers:
                pushed_layers.append(digest)
            continue

    cached_steps = [info for info in sorted(steps.values(), key=lambda s: s.num) if info.cached]
    executed_steps = [
        info
        for info in sorted(steps.values(), key=lambda s: s.num)
        if not info.cached and info.command
    ]

    return CacheReport(
        job=job,
        cached_steps=cached_steps,
        executed_steps=executed_steps,
        existing_layers=existing_layers,
        pushed_layers=pushed_layers,
    )


def load_lines(source: Optional[str]) -> Sequence[str]:
    if source:
        return Path(source).read_text(encoding="utf-8").splitlines()
    return [line.rstrip("\n") for line in iter(input, "")]


def main() -> None:
    parser = argparse.ArgumentParser(description="Summarize devcontainer cache usage from a GitHub Actions log.")
    parser.add_argument("--log", help="Path to the log file. Reads from stdin if omitted.")
    parser.add_argument("--job", default="build-devcontainer", help="Job name prefix in the log (default: build-devcontainer).")
    parser.add_argument("--json", action="store_true", help="Emit JSON instead of human-readable output.")
    parser.add_argument("--markdown", action="store_true", help="Emit Markdown summary.")

    args = parser.parse_args()

    lines = load_lines(args.log)
    report = parse_lines(lines, job=args.job)

    if args.json:
        print(json.dumps(report.to_dict(), indent=2))
    elif args.markdown:
        print(report.to_markdown())
    else:
        print(f"Job: {report.job}")
        print(f"- BuildKit cache hits: {len(report.cached_steps)} ({', '.join(f'#{s.num}' for s in report.cached_steps) or 'none'})")
        print(f"- BuildKit executed steps: {len(report.executed_steps)} ({', '.join(f'#{s.num}' for s in report.executed_steps) or 'none'})")
        print(f"- Registry layers reused: {len(report.existing_layers)}")
        print(f"- Registry layers pushed: {len(report.pushed_layers)}")
        if report.cached_steps:
            print("\nCached steps:")
            for step in report.cached_steps:
                print(f"  - #{step.num}: {step.label()}")
        if report.executed_steps:
            print("\nExecuted steps:")
            for step in report.executed_steps:
                duration = f"{step.duration:g}{step.duration_unit or ''}" if step.duration is not None else ""
                print(f"  - #{step.num}: {step.label()} {duration}")
        if report.existing_layers:
            print("\nReused layers:")
            print("  " + ", ".join(report.existing_layers))
        if report.pushed_layers:
            print("\nPushed layers:")
            print("  " + ", ".join(report.pushed_layers))


if __name__ == "__main__":
    main()
