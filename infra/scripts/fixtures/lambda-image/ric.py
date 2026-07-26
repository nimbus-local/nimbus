"""Minimal Lambda runtime interface client.

A container-image Lambda is not an HTTP server: the process inside polls the
Lambda Runtime API for work and posts results back. Production images use the
`awslambdaric` package for this; this fixture implements the same loop in the
standard library so the smoke test needs no wheels and no network at build
time.

The handler echoes its event back, along with the AWS endpoint it was given, so
the test can confirm Nimbus injected the container's environment.
"""

import json
import os
import urllib.request

API = os.environ["AWS_LAMBDA_RUNTIME_API"]
BASE = "http://{}/2018-06-01/runtime".format(API)


def handle(event):
    return {
        "echo": event,
        "endpoint": os.environ.get("AWS_ENDPOINT_URL", ""),
        "marker": os.environ.get("NIMBUS_SMOKE_MARKER", ""),
    }


def main():
    while True:
        with urllib.request.urlopen(BASE + "/invocation/next") as resp:
            request_id = resp.headers["Lambda-Runtime-Aws-Request-Id"]
            raw = resp.read()

        try:
            event = json.loads(raw) if raw else None
        except ValueError:
            event = None

        body = json.dumps(handle(event)).encode()
        req = urllib.request.Request(
            "{}/invocation/{}/response".format(BASE, request_id),
            data=body,
            method="POST",
        )
        urllib.request.urlopen(req).read()


if __name__ == "__main__":
    main()
