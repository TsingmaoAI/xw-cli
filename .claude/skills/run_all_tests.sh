#!/bin/bash
# Run all XW-CLI test skills

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

SKILLS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOTAL=0
PASSED=0
FAILED=0

echo ""
echo -e "${CYAN}${BOLD}╔════════════════════════════════════════╗${NC}"
echo -e "${CYAN}${BOLD}║   XW-CLI Test Skills Runner           ║${NC}"
echo -e "${CYAN}${BOLD}╚════════════════════════════════════════╝${NC}"
echo ""

# Find all test scripts
for script in "$SKILLS_DIR"/test-*/scripts/*.sh; do
    [ -f "$script" ] || continue
    
    SKILL_NAME=$(basename "$(dirname "$(dirname "$script")")")
    TOTAL=$((TOTAL + 1))
    
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BOLD}Running: $SKILL_NAME${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    
    if bash "$script"; then
        PASSED=$((PASSED + 1))
        echo -e "${GREEN}✓ $SKILL_NAME PASSED${NC}"
    else
        FAILED=$((FAILED + 1))
        echo -e "${RED}✗ $SKILL_NAME FAILED${NC}"
    fi
    
    echo ""
done

# Final summary
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BOLD}${CYAN}           FINAL SUMMARY                ${NC}"
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "  Total Skills:    $TOTAL"
echo -e "  ${GREEN}Passed:${NC}          $PASSED"
echo -e "  ${RED}Failed:${NC}          $FAILED"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}${BOLD}✓ All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}${BOLD}✗ Some tests failed${NC}"
    exit 1
fi
