#!/bin/bash
# Test xw device commands

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASSED=0
TOTAL=0

test_run() {
    TOTAL=$((TOTAL + 1))
    echo -e "${BLUE}[TEST $TOTAL]${NC} $1"
}

test_pass() {
    PASSED=$((PASSED + 1))
    echo -e "${GREEN}✓${NC} $1"
}

test_fail() {
    echo -e "${RED}✗${NC} $1"
}

test_info() {
    echo -e "${YELLOW}ℹ${NC} $1"
}

echo ""
echo "========================================="
echo "XW Device Commands Tests"
echo "========================================="
echo ""

# Test 1: xw device list
test_run "xw device list"
DEVICE_OUTPUT=$(xw device list 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    # Extract device count
    if echo "$DEVICE_OUTPUT" | grep -q "Total:.*AI chip"; then
        COUNT=$(echo "$DEVICE_OUTPUT" | grep "Total:.*AI chip" | grep -o '[0-9]\+' | head -1)
        test_info "Detected $COUNT AI chip(s)"
        
        # Extract device types
        TYPES=$(echo "$DEVICE_OUTPUT" | grep -v "^CHIP KEY" | grep -v "^-" | grep -v "^Total" | grep -v "^$" | awk '{print $1}' | sort -u | tr '\n' ' ')
        test_info "Device types: $TYPES"
        
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
    
    # Check PCI info
    if echo "$DEVICE_OUTPUT" | grep -q "0x"; then
        test_pass "PCI information present"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 2: xw device supported
test_run "xw device supported"
SUPPORTED_OUTPUT=$(xw device supported 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    if echo "$SUPPORTED_OUTPUT" | grep -q "Total:.*chip model"; then
        COUNT=$(echo "$SUPPORTED_OUTPUT" | grep "Total:.*chip model" | grep -o '[0-9]\+' | head -1)
        test_info "$COUNT chip model(s) supported"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
