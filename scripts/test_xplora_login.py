#!/usr/bin/env python3
"""Standalone Xplora login probe — no dependencies beyond the stdlib.

Run this ON THE SAME VPS as the bridge. It replicates the EXACT request the
bridge now sends (MD5 password, Chrome User-Agent, H-* headers, operationName)
so we can see whether the failure is in our code/environment or server-side.

Your credentials are read from a prompt and never stored or printed.

Usage:
    python3 test_xplora_login.py

It will:
  1. Try PHONE login (country code + phone + password).
  2. Optionally try EMAIL login (to test the "Gmail" hypothesis).
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


def call(variables: dict) -> None:
    body = json.dumps(
        {
            "query": SIGN_IN,
            "variables": variables,
            "operationName": "signInWithEmailOrPhone",
        }
    ).encode()

    now = datetime.now(timezone.utc)
    headers = {
        "Content-Type": "application/json; charset=UTF-8",
        "User-Agent": USER_AGENT,
        "H-Date": now.strftime("%a, %d %b %Y %H:%M:%S") + " GMT",
        "H-Tid": str(math.floor(time.time())),
        "H-BackDoor-Authorization": f"Open {API_KEY}:{API_SECRET}",
    }

    # Redacted echo of what we send (password hash and value hidden).
    safe_vars = dict(variables)
    if "password" in safe_vars:
        safe_vars["password"] = "<md5:%d chars>" % len(safe_vars["password"])
    print("  request variables:", json.dumps(safe_vars))

    req = urllib.request.Request(ENDPOINT, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            status = resp.status
            text = resp.read().decode()
    except urllib.error.HTTPError as e:
        status = e.code
        text = e.read().decode()
    except Exception as e:  # noqa: BLE001
        print("  TRANSPORT ERROR:", e)
        return

    print("  HTTP", status)
    print("  body:", text.strip())
    try:
        data = json.loads(text)
        errs = data.get("errors")
        if errs:
            print("  >>> ERROR CODE:", errs[0].get("code"), "-", errs[0].get("message"))
        elif data.get("data", {}).get("signInWithEmailOrPhone"):
            print("  >>> SUCCESS: got a token!")
    except json.JSONDecodeError:
        pass


def main() -> None:
    print("=== Xplora login probe ===\n")
    password = getpass.getpass("Password (hidden): ")
    pw_md5 = hashlib.md5(password.encode()).hexdigest()

    cc = input("Country code (e.g. 47): ").strip()
    phone = input("Phone number (without country code): ").strip()

    print("\n[1] PHONE + MD5 password:")
    call(
        {
            "countryPhoneNumber": cc,
            "phoneNumber": phone,
            "password": pw_md5,
            "emailAddress": None,
            "client": "APP",
            "userLang": "en",
            "timeZone": "UTC",
        }
    )

    print("\n[2] PHONE + PLAINTEXT password:")
    call(
        {
            "countryPhoneNumber": cc,
            "phoneNumber": phone,
            "password": password,
            "emailAddress": None,
            "client": "APP",
            "userLang": "en",
            "timeZone": "UTC",
        }
    )

    email = input("\nEmail address (blank to skip): ").strip()
    if email:
        print("\n[3] EMAIL + MD5 password:")
        call(
            {
                "countryPhoneNumber": None,
                "phoneNumber": None,
                "password": pw_md5,
                "emailAddress": email,
                "client": "APP",
                "userLang": "en",
                "timeZone": "UTC",
            }
        )
        print("\n[4] EMAIL + PLAINTEXT password:")
        call(
            {
                "countryPhoneNumber": None,
                "phoneNumber": None,
                "password": password,
                "emailAddress": email,
                "client": "APP",
                "userLang": "en",
                "timeZone": "UTC",
            }
        )


if __name__ == "__main__":
    main()
