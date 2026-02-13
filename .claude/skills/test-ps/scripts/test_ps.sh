#!/bin/bash
# Test xw ps command

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
echo "XW PS Command Tests"
echo "========================================="
echo ""

# Test 1: xw ps
test_run "xw ps"
OUTPUT=$(xw ps 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    # Check for table header
    if echo "$OUTPUT" | grep -q "ALIAS.*MODEL.*ENGINE"; then
        test_pass "Table format correct"
        
        # Count instances (excluding header)
        COUNT=$(echo "$OUTPUT" | grep -v "ALIAS.*MODEL" | grep -v "^$" | wc -l)
        test_info "Found $COUNT instance(s)"
        
        # Check for valid states
        if echo "$OUTPUT" | grep -qE "ready|unhealthy|starting|error"; then
            test_pass "Instance states present"
            PASSED=$((PASSED + 1))
            TOTAL=$((TOTAL + 1))
        fi
        
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    elif echo "$OUTPUT" | grep -qi "no instances"; then
        test_info "No instances found (expected)"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 2: xw ps -a
test_run "xw ps -a (all)"
if xw ps -a >/dev/null 2>&1; then
    test_pass "Flag -a works"
else
    test_fail "Flag -a failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
