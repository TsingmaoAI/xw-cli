#!/bin/bash
# Test xw config commands

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
echo "XW Config Commands Tests"
echo "========================================="
echo ""

# Test 1: xw config info
test_run "xw config info"
OUTPUT=$(xw config info 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    # Check for configuration keys
    if echo "$OUTPUT" | grep -qE "name|registry|version"; then
        test_pass "Configuration information present"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 2: xw config get name
test_run "xw config get name"
if OUTPUT=$(xw config get name 2>&1); then
    test_pass "Get name command works"
    test_info "Server name: $OUTPUT"
    PASSED=$((PASSED + 1))
    TOTAL=$((TOTAL + 1))
else
    test_fail "Command failed"
fi
echo ""

# Test 3: xw config get registry
test_run "xw config get registry"
if OUTPUT=$(xw config get registry 2>&1); then
    test_pass "Get registry command works"
    PASSED=$((PASSED + 1))
    TOTAL=$((TOTAL + 1))
else
    test_fail "Command failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
