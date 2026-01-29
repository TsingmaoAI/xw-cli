# XW - AI Inference Platform for Domestic Chips

XW is a high-performance AI inference platform optimized for domestic accelerators, providing unified access to large language models across different hardware backends.

## Overview

XW simplifies LLM deployment and inference on domestic chips by providing:

- **Unified Interface**: Single CLI and API for model management across different hardware
- **Multiple Backends**: Support for vLLM and MindIE inference engines
- **Hardware Optimization**: Native support for Ascend NPU with automatic device allocation
- **Docker Integration**: Containerized deployment with proper device isolation and resource management
- **Model Registry**: Built-in catalog of popular models (Qwen series, etc.)
- **Production Ready**: Systemd service, health checks, and comprehensive logging

## Supported Hardware

- **Ascend NPU** (Huawei) - 910B, 800I-A2, and other models
- More accelerators coming soon

## Supported Inference Engines

- **vLLM** - High-throughput serving with PagedAttention
- **MindIE** - Huawei's optimized inference engine for Ascend NPUs

## Quick Start

### Installation

```bash
# Download and extract
tar -xzf xw-1.0.0-amd64.tar.gz
cd xw-1.0.0-amd64

# Install
sudo bash scripts/install.sh

# Start server
sudo systemctl start xw-server
sudo systemctl enable xw-server
```

### Basic Usage

```bash
# Check detected hardware
xw device list

# List downloaded models
xw ls

# Run a model (starts instance and enters interactive chat)
xw run qwen2-7b

# Check running instances
xw ps

# Stop an instance
xw stop qwen2-7b

# View logs
xw logs qwen2-7b
```

### API Usage

XW server provides OpenAI-compatible API on port 11581:

```bash
# Health check
curl http://localhost:11581/api/health

# Chat completion (OpenAI compatible)
curl http://localhost:11581/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen2-7b",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": false
  }'
```

## Architecture

XW uses a modular architecture with clear separation of concerns:

- **CLI Client**: Communicates with server via REST API (default: `http://localhost:11581`)
- **Server**: Manages model lifecycle, device allocation, and inference routing
- **Runtime Manager**: Orchestrates different inference backends (vLLM, MindIE)
- **Device Manager**: Automatic hardware detection and NPU allocation
- **Docker Backend**: Isolated containers with dedicated device access

Each model instance runs in a dedicated Docker container with:
- Exclusive NPU allocation (no sharing between instances)
- Automatic port management and health monitoring
- Full isolation for security and resource management
- Persistent logging for debugging and profiling

## Building from Source

```bash
# Build binary
make build

# Run tests
make test

# Build release packages for distribution
make package
```

## Configuration

XW server can be configured via command-line flags:

```bash
# Custom port and host
xw serve --host 0.0.0.0 --port 8080

# Verbose logging
xw serve -v
```

Environment variables:
- `XW_SERVER`: Server URL for CLI client (default: `http://localhost:11581`)
- `XW_HOME`: Data directory (default: `/opt/xw`)
- `XW_LOG_LEVEL`: Log level (default: `info`)

## License

Apache License 2.0

## Documentation

For more information, run:

```bash
xw --help
xw <command> --help
```
