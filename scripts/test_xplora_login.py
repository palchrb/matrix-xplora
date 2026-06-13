#!/usr/bin/env python3
"""Xplora login probe — tests multiple password formats to find which one works.

Run on any machine with Python 3 (no extra dependencies).
Credentials are read interactively and never stored or echoed.

Usage:
    python3 scripts/test_xplora_login.py
"""

import getpass
import hashlib
import json
import math
import time
import urllib.request
from datetime import datetime, timezone

ENDPOINT = "https://api.myxplora.com/api"
API_KEY = "fc45d50304511edbf67a12b93c413b6a"
API_SECRET = "1e9b6fe0327711ed959359c157878dcb"
USER_AGENT = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.3"
)

SIGN_IN = (
    "mutation signInWithEmailOrPhone("
    "$countryPhoneNumber: String, $phoneNumber: String, $password: String!, "
    "$emailAddress: String, $client: ClientType!, $userLang: String!, "
    "$timeZone: String!) {\n"
    "  signInWithEmailOrPhone(countryPhoneNumber: $countryPhoneNumber, "
    "phoneNumber: $phoneNumber, password: $password, emailAddress: $emailAddress, "
    "client: $client, userLang: $userLang, timeZone: $timeZone) {\n"
    "    id\n    token\n    refreshToken\n    expireDate\n    user { id }\n"
    "    w360 { token secret }\n  }\n}"
)


def call(label: str, variables: dict) -> bool:
    pw_len = len(variables.get("password", ""))
    safe = {k: (f"<{pw_len} chars>" if k == "password" else v) for k, v in variables.items()}
    print(f"\n[{label}]")
    print("  vars:", json.dumps(safe))

    body = json.dumps(
        {"query": SIGN_IN, "variables": variables, "operationName": "signInWithEmailOrPhone"}
    ).encode()

    now = datetime.now(timezone.utc)
    headers = {
        "Content-Type": "application/json; charset=UTF-8",
        "User-Agent": USER_AGENT,
        "H-Date": now.strftime("%a, %d %b %Y %H:%M:%S") + " GMT",
        "H-Tid": str(math.floor(time.time())),
        "H-BackDoor-Authorization": f"Open {API_KEY}:{API_SECRET}",
    }

    req = urllib.request.Request(ENDPOINT, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            text = resp.read().decode()
    except urllib.error.HTTPError as e:
        text = e.read().decode()
    except Exception as exc:
        print("  TRANSPORT ERROR:", exc)
        return False

    try:
        data = json.loads(text)
        errs = data.get("errors")
        if errs:
            print(f"  FAIL  {errs[0].get('code')} — {errs[0].get('message')}")
            return False
        if data.get("data", {}).get("signInWithEmailOrPhone"):
            print("  SUCCESS — got a token!")
            return True
    except json.JSONDecodeError:
        print("  body:", text.strip())
    return False


def base_vars(cc: str, phone: str, pw: str) -> dict:
    return {
        "countryPhoneNumber": cc,
        "phoneNumber": phone,
        "password": pw,
        "emailAddress": None,
        "client": "APP",
        "userLang": "en",
        "timeZone": "UTC",
    }


def main() -> None:
    print("=== Xplora login probe ===\n")
    password = getpass.getpass("Password (hidden): ")
    cc = input("Country code (e.g. 47): ").strip()
    phone = input("Phone number (without country code): ").strip()

    pw_md5    = hashlib.md5(password.encode()).hexdigest()
    pw_sha256 = hashlib.sha256(password.encode()).hexdigest()
    pw_sha1   = hashlib.sha1(password.encode()).hexdigest()  # noqa: S324

    candidates = [
        ("MD5-lower",  pw_md5),
        ("MD5-upper",  pw_md5.upper()),
        ("SHA-256",    pw_sha256),
        ("SHA-1",      pw_sha1),
        ("plaintext",  password),
    ]

    for label, pw in candidates:
        if call(label, base_vars(cc, phone, pw)):
            print(f"\n>>> Winner: {label}. Update the bridge to use this format.")
            return

    print("\nAll phone variants failed. Trying email…")
    email = input("Email address linked to Xplora account: ").strip()
    if not email:
        print("Skipped.")
        return

    for label, pw in candidates:
        ev = {
            "countryPhoneNumber": None,
            "phoneNumber": None,
            "password": pw,
            "emailAddress": email,
            "client": "APP",
            "userLang": "en",
            "timeZone": "UTC",
        }
        if call(f"EMAIL+{label}", ev):
            print(f"\n>>> Winner: EMAIL + {label}. Update bridge to use email login.")
            return

    print("\nAll variants failed. Either lockout is still active or a different auth flow is needed.")


if __name__ == "__main__":
    main()
