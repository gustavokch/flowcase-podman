#!/usr/bin/env python3
"""Decrypt a guac token produced by EncryptGuacToken (T2.17).

Same primitive the legacy orchestrator used at flowcase/routes/droplet.py:
AES-256-CBC + PKCS#7 with a 16-byte IV, wrapped in a JSON envelope and
base64'd.

Usage:
    decrypt_token.py <key32> <token-base64>

Prints the decrypted inner JSON to stdout. Exits non-zero on any
decryption / parse failure.

Requires pycryptodome:
    pip install pycryptodome
"""

import base64
import json
import sys

from Crypto.Cipher import AES
from Crypto.Util.Padding import unpad


def main(argv):
    if len(argv) != 3:
        print("usage: decrypt_token.py <key32> <token-base64>", file=sys.stderr)
        return 2

    key = argv[1].encode()
    if len(key) < 32:
        print(f"key must be at least 32 bytes, got {len(key)}", file=sys.stderr)
        return 2
    key = key[:32]

    token = argv[2]

    envelope_bytes = base64.b64decode(token)
    envelope = json.loads(envelope_bytes)
    iv = base64.b64decode(envelope["iv"])
    if len(iv) != 16:
        print(f"IV must be 16 bytes, got {len(iv)}", file=sys.stderr)
        return 2
    ciphertext = base64.b64decode(envelope["value"])

    cipher = AES.new(key, AES.MODE_CBC, iv)
    plaintext = unpad(cipher.decrypt(ciphertext), AES.block_size)
    sys.stdout.write(plaintext.decode("utf-8"))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
