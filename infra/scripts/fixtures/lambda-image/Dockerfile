# Smoke-test fixture: a container-image Lambda built on a plain language base,
# the way a real project's image is built. It deliberately does NOT bundle the
# runtime interface emulator — Nimbus injects that at start, and this image
# proves that path works.
#
# The entrypoint is what Nimbus has to recover via `docker inspect` and hand to
# the emulator, since overriding --entrypoint to inject it discards the
# image's own.
FROM public.ecr.aws/docker/library/python:3.13-slim

COPY ric.py /ric.py

ENTRYPOINT ["python", "/ric.py"]
