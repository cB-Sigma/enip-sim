# Copyright 2026 Abhinav Gautam
# SPDX-License-Identifier: Apache-2.0
#
# pycomm3 (MIT, github.com/ottowayi/pycomm3) as an interop peer; it is
# installed from PyPI at build time, not redistributed.
FROM python:3.12.10-slim
RUN pip install --no-cache-dir pycomm3==1.2.16
COPY client.py /peer/client.py
WORKDIR /peer
