import json
import subprocess
import sys
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("devcontainer_cache_report.py")


class DevcontainerCacheReportCLITest(unittest.TestCase):
    def test_reads_log_from_stdin(self) -> None:
        log = "\n".join(
            [
                "build-devcontainer\tstep\t#11 [stage] RUN echo hi",
                "build-devcontainer\tstep\t#11 DONE 1.2s",
                "build-devcontainer\tstep\tabc123def456: Layer already exists",
                "",
            ]
        )

        result = subprocess.run(
            [sys.executable, str(SCRIPT_PATH), "--job", "build-devcontainer", "--json"],
            input=log,
            capture_output=True,
            text=True,
            check=False,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report["job"], "build-devcontainer")
        self.assertEqual([step["step"] for step in report["executed_steps"]], [11])
        self.assertEqual(report["existing_layers"], ["abc123def456"])


if __name__ == "__main__":
    unittest.main()
