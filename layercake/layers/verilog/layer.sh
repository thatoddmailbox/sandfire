#!/bin/bash
set -e

# Install Verilog development tools

apt-get update
apt-get install -y \
    build-essential \
    iverilog \
    verilator

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
