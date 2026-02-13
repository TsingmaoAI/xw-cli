#!/bin/bash
# Test xw ls command

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
echo "XW List Models Tests"
echo "========================================="
echo ""

# Test 1: xw ls
test_run "xw ls"
OUTPUT=$(xw ls 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    # Check for table header or empty message
    if echo "$OUTPUT" | grep -q "MODEL.*SOURCE"; then
        test_pass "Downloaded models table format correct"
        # Count models (lines with content, excluding header)
        COUNT=$(echo "$OUTPUT" | grep -v "MODEL.*SOURCE" | grep -v "^$" | grep -v "^─" | wc -l)
        test_info "Found $COUNT downloaded model(s)"
        PASSED=$((PASSED + 2))
        TOTAL=$((TOTAL + 2))
    elif echo "$OUTPUT" | grep -qi "no models"; then
        test_info "No models downloaded (expected)"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 2: xw ls -a
test_run "xw ls -a (all models)"
OUTPUT=$(xw ls -a 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    if echo "$OUTPUT" | grep -q "MODEL.*SOURCE"; then
        test_pass "All models table format correct"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
fi
echo ""

# Test 3: xw list (alias)
test_run "xw list (alias)"
if xw list >/dev/null 2>&1; then
    test_pass "Alias 'list' works"
else
    test_fail "Alias failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
