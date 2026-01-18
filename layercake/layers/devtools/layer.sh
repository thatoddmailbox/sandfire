#!/bin/bash
set -e

# Install development tools

apt-get update
apt-get install -y \
    build-essential \
    cmake \
    git \
    vim \
    gdb \
    valgrind \
    pkg-config

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
