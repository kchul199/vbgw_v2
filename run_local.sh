#!/bin/bash

# VBGW Local Process Runner
# This script starts the AI Engine, Bridge, and Orchestrator as local processes.
# Ensure Redis (6379) and FreeSwitch (8021) are running.

mkdir -p logs

# 1. AI Engine
echo "Starting VBGW AI Engine..."
cd vbgw-ai
export PORT=8091
export LOG_LEVEL=debug
./ai_engine > ../logs/ai.log 2>&1 &
AI_PID=$!
cd ..

# 2. Bridge
echo "Starting Bridge..."
cd vbgw-freeswitch/bridge
export WS_PORT=8090
export INTERNAL_PORT=8091
export AI_GRPC_ADDR=localhost:8091
export ONNX_MODEL_PATH=$(pwd)/../models/silero_vad.onnx
export LOG_LEVEL=debug
./bridge_app > ../../logs/bridge.log 2>&1 &
BRIDGE_PID=$!
cd ../..

# 3. Orchestrator
echo "Starting Orchestrator..."
cd vbgw-freeswitch/orchestrator
export HTTP_PORT=8080
export ESL_HOST=localhost
export ESL_PORT=8021
export ESL_PASSWORD=ClueConSecretDev
export REDIS_ADDR=localhost:6379
export BRIDGE_HOST=localhost
export BRIDGE_INTERNAL_PORT=8091
export LOG_LEVEL=debug
./orch_app > ../../logs/orchestrator.log 2>&1 &
ORCH_PID=$!
cd ../..

echo "Services started:"
echo "  AI Engine (Port 8091, PID $AI_PID)"
echo "  Bridge (Port 8090/8091, PID $BRIDGE_PID)"
echo "  Orchestrator (Port 8080, PID $ORCH_PID)"
echo ""
echo "Logs are available in logs/"
echo "To stop all services: kill $AI_PID $BRIDGE_PID $ORCH_PID"
