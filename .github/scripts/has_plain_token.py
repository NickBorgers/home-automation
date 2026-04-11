#!/usr/bin/env python3
"""
Check whether a given trigger token appears in a text body as plain prose —
i.e. not inside fenced code blocks, inline code, HTML <code> blocks, or
markdown/HTML links. Used by the AI Assistant workflow's authorize job to
decide whether a comment / issue body is a genuine /autoresolve invocation
or just a quoted example.

Usage:
    printf %s "$BODY" | python3 .github/scripts/has_plain_token.py <token>

The token is passed as the first positional argument (e.g. '/autoresolve').
Body text is read from stdin so multi-line / special-character content is
safe without shell escaping headaches.

Prints 'true' or 'false' on stdout. Exits 0 in both cases. Exits non-zero
only on usage errors (missing argument).
"""

import re
import sys


def strip_code(text: str) -> str:
    """Remove fenced code blocks, inline code, and HTML <code> blocks."""
    text = re.sub(r"```.*?```", " ", text, flags=re.DOTALL | re.IGNORECASE)
    text = re.sub(r"<code>.*?</code>", " ", text, flags=re.DOTALL | re.IGNORECASE)
    text = re.sub(r"`[^`]*`", " ", text)
    return text


def strip_links(text: str) -> str:
    """Remove markdown links [text](url) and HTML <a>...</a> links."""
    text = re.sub(r"\[[^\]]*\]\([^)]+\)", " ", text)
    text = re.sub(r"<a\s[^>]*>.*?</a>", " ", text, flags=re.DOTALL | re.IGNORECASE)
    return text


def has_plain(text: str, token: str) -> bool:
    sanitized = strip_links(strip_code(text))
    # Require the token to be delimited: nothing alphanumeric or '-' / '_'
    # immediately before or after. Also excludes '@', '[', '/', backtick,
    # single/double quote as leading characters so mentions embedded in
    # nested quoting (e.g. `\"/autoresolve\"` in an already-escaped string)
    # don't false-positive.
    pattern = re.compile(
        r"(?<![\w@\[/`\"'])" + re.escape(token) + r"(?![\w-])",
        re.IGNORECASE,
    )
    return bool(pattern.search(sanitized))


def main() -> int:
    if len(sys.argv) != 2:
        print("Usage: has_plain_token.py <token>", file=sys.stderr)
        return 2
    token = sys.argv[1]
    body = sys.stdin.read()
    print("true" if has_plain(body, token) else "false")
    return 0


if __name__ == "__main__":
    sys.exit(main())
