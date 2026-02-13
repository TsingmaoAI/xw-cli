#!/bin/bash
# Test xw version command

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
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

echo ""
echo "========================================="
echo "XW Version Command Tests"
echo "========================================="
echo ""

# Test 1: xw version
test_run "xw version"
if OUTPUT=$(xw version 2>&1); then
    test_pass "Command executed successfully"
    if echo "$OUTPUT" | grep -qi "version"; then
        test_pass "Version information present"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 2: xw version --client
test_run "xw version --client"
if OUTPUT=$(xw version --client 2>&1); then
    test_pass "Client version flag works"
else
    test_fail "Client version flag failed"
fi
echo ""

# Test 3: xw version --server
test_run "xw version --server"
if OUTPUT=$(xw version --server 2>&1); then
    test_pass "Server version flag works"
else
    test_fail "Server version flag failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
