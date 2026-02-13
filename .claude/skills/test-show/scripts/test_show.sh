#!/bin/bash
# Test xw show command

MODEL="${1:-qwen3-8b}"

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
echo "XW Show Model Tests"
echo "========================================="
echo ""
test_info "Testing with model: $MODEL"
echo ""

# Test 1: xw show MODEL
test_run "xw show $MODEL"
OUTPUT=$(xw show "$MODEL" 2>&1)
if [ $? -eq 0 ]; then
    test_pass "Command executed successfully"
    
    # Check for model information
    if echo "$OUTPUT" | grep -qi "model\|parameter\|license"; then
        test_pass "Model information present"
        PASSED=$((PASSED + 1))
        TOTAL=$((TOTAL + 1))
    fi
else
    test_fail "Command failed"
    test_info "Model may not be available"
fi
echo ""

# Test 2: xw show MODEL --engines
test_run "xw show $MODEL --engines"
if xw show "$MODEL" --engines >/dev/null 2>&1; then
    test_pass "Engines flag works"
else
    test_fail "Engines flag failed"
fi
echo ""

# Test 3: xw show MODEL --parameters
test_run "xw show $MODEL --parameters"
if xw show "$MODEL" --parameters >/dev/null 2>&1; then
    test_pass "Parameters flag works"
else
    test_fail "Parameters flag failed"
fi
echo ""

# Test 4: xw show MODEL --license
test_run "xw show $MODEL --license"
if xw show "$MODEL" --license >/dev/null 2>&1; then
    test_pass "License flag works"
else
    test_fail "License flag failed"
fi
echo ""

echo "========================================="
echo -e "${GREEN}Results: $PASSED/$TOTAL tests passed${NC}"
echo "========================================="
echo ""

[ $PASSED -ge $TOTAL ] && exit 0 || exit 1
