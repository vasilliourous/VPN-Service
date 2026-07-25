#!/usr/bin/env bash
# MyVPN Code Card Printer
# Formats activation codes into printable card sheets (PDF via enscript/ps2pdf).
#
# Usage:
#   ./scripts/print_codes.sh <codes_file> [output.pdf]
#
# Examples:
#   ./scripts/print_codes.sh eco-codes.txt eco-cards.pdf
#   cat codes.txt | ./scripts/print_codes.sh
#
# Dependencies (any one):
#   - enscript + ps2pdf (recommended, apt-get install enscript ghostscript)
#   - pandoc + wkhtmltopdf (alternative)
#   - a2ps (alternative)

set -euo pipefail

INPUT_FILE="${1:-}"
OUTPUT_FILE="${2:-myvpn-cards.pdf}"

# ── Detect available tools ──
HAVE_ENSCRIPT=0
HAVE_PANDOC=0
HAVE_A2PS=0

command -v enscript &>/dev/null && HAVE_ENSCRIPT=1
command -v pandoc &>/dev/null && HAVE_PANDOC=1
command -v a2ps &>/dev/null && HAVE_A2PS=1

usage() {
    echo "Usage: $0 <codes_file> [output.pdf]"
    echo ""
    echo "Arguments:"
    echo "  codes_file   File with one activation code per line"
    echo "  output.pdf   Output PDF file (default: myvpn-cards.pdf)"
    echo ""
    echo "Piped input:"
    echo "  cat codes.txt | $0  (requires -o or uses default output)"
    echo ""
    echo "Dependencies:"
    echo "  Recommended: enscript + ghostscript (apt-get install enscript ghostscript)"
    echo "  Alternative: pandoc + wkhtmltopdf"
    echo "  Alternative: a2ps + ghostscript"
    exit 1
}

# ── Read codes ──
if [ -n "$INPUT_FILE" ]; then
    if [ ! -f "$INPUT_FILE" ]; then
        echo "Error: File not found: ${INPUT_FILE}"
        usage
    fi
    # Read non-empty lines, skip comments
    mapfile -t CODES < <(grep -v '^#' "$INPUT_FILE" | grep -v '^$' || true)
elif [ ! -t 0 ]; then
    # Read from stdin
    mapfile -t CODES < <(grep -v '^#' | grep -v '^$' || true)
else
    echo "Error: No input file specified and no pipe input detected."
    usage
fi

if [ ${#CODES[@]} -eq 0 ]; then
    echo "Error: No codes found in input."
    exit 1
fi

echo "═══════════════════════════════════════════"
echo " MyVPN Code Card Printer"
echo "═══════════════════════════════════════════"
echo " Codes:      ${#CODES[@]}"
echo " Output:     ${OUTPUT_FILE}"
echo " Method:     $([ "$HAVE_ENSCRIPT" = 1 ] && echo 'enscript + ps2pdf' || \
                     [ "$HAVE_PANDOC" = 1 ] && echo 'pandoc + wkhtmltopdf' || \
                     [ "$HAVE_A2PS" = 1 ] && echo 'a2ps + ps2pdf' || \
                     echo 'plain text (no PDF tools found)')"
echo "═══════════════════════════════════════════"

# ── Method 1: enscript + ps2pdf (best quality) ──
generate_enscript() {
    local tmp_ps
    tmp_ps=$(mktemp /tmp/myvpn-cards-XXXXXX.ps)

    # Build enscript input with card formatting
    local tmp_txt
    tmp_txt=$(mktemp /tmp/myvpn-cards-XXXXXX.txt)

    echo "" > "$tmp_txt"
    for code in "${CODES[@]}"; do
        # Extract tier from filename or mark as unknown
        local tier=""
        if [[ "$INPUT_FILE" == *"eco"* ]]; then tier="Eco"
        elif [[ "$INPUT_FILE" == *"stealth"* ]]; then tier="Stealth"
        elif [[ "$INPUT_FILE" == *"strike"* ]]; then tier="Strike"
        else tier="MyVPN"
        fi

        cat >> "$tmp_txt" << CARD

╔══════════════════════════════════╗
║                                  ║
║         M Y V P N                ║
║         ${tier} TIER              ║
║                                  ║
║   ${code}   ║
║                                  ║
║   myvpn://activate/${code}       ║
║                                  ║
║   Scratch to reveal              ║
║   One-time activation            ║
║                                  ║
╚══════════════════════════════════╝

CARD
    done

    enscript -B -f Courier-Bold10 --header='' --footer='' \
        -o "$tmp_ps" "$tmp_txt" 2>/dev/null || {
        rm -f "$tmp_ps" "$tmp_txt"
        return 1
    }

    ps2pdf "$tmp_ps" "$OUTPUT_FILE" 2>/dev/null || {
        # Try gs directly as fallback
        gs -q -dNOPAUSE -dBATCH -sDEVICE=pdfwrite \
           -sOutputFile="$OUTPUT_FILE" "$tmp_ps" 2>/dev/null || {
            rm -f "$tmp_ps" "$tmp_txt"
            return 1
        }
    }

    rm -f "$tmp_ps" "$tmp_txt"
    return 0
}

# ── Method 2: a2ps + ps2pdf ──
generate_a2ps() {
    local tmp_ps
    tmp_ps=$(mktemp /tmp/myvpn-cards-XXXXXX.ps)

    # Create a formatted input
    local tmp_txt
    tmp_txt=$(mktemp /tmp/myvpn-cards-XXXXXX.txt)

    echo "" > "$tmp_txt"
    for code in "${CODES[@]}"; do
        echo "========================================" >> "$tmp_txt"
        echo "  M Y V P N" >> "$tmp_txt"
        echo "" >> "$tmp_txt"
        echo "  $code" >> "$tmp_txt"
        echo "" >> "$tmp_txt"
        echo "  myvpn://activate/$code" >> "$tmp_txt"
        echo "========================================" >> "$tmp_txt"
        echo "" >> "$tmp_txt"
    done

    a2ps -B -1 --medium=A4 -o "$tmp_ps" "$tmp_txt" 2>/dev/null || {
        rm -f "$tmp_ps" "$tmp_txt"
        return 1
    }

    ps2pdf "$tmp_ps" "$OUTPUT_FILE" 2>/dev/null || {
        rm -f "$tmp_ps" "$tmp_txt"
        return 1
    }

    rm -f "$tmp_ps" "$tmp_txt"
    return 0
}

# ── Method 3: Plain text output (always works) ──
generate_text() {
    local txt_output="${OUTPUT_FILE%.pdf}.txt"

    echo "MyVPN Activation Codes" > "$txt_output"
    echo "Generated: $(date)" >> "$txt_output"
    echo "═══════════════════════════════════════════" >> "$txt_output"
    echo "" >> "$txt_output"

    local i=1
    for code in "${CODES[@]}"; do
        echo "Card $i:" >> "$txt_output"
        echo "  Code: ${code}" >> "$txt_output"
        echo "  URL:  myvpn://activate/${code}" >> "$txt_output"
        echo "" >> "$txt_output"
        i=$((i + 1))
    done

    echo "✓ Codes written to ${txt_output}"
    echo "  (PDF generation not available — install enscript or pandoc)"
    return 0
}

# ── Try methods in order of preference ──
GENERATED=0
if [ "$HAVE_ENSCRIPT" = 1 ]; then
    if generate_enscript; then
        GENERATED=1
        echo "✓ PDF generated with enscript → ${OUTPUT_FILE}"
    else
        echo "  enscript method failed, trying alternatives..."
    fi
fi

if [ "$GENERATED" -eq 0 ] && [ "$HAVE_A2PS" = 1 ]; then
    if generate_a2ps; then
        GENERATED=1
        echo "✓ PDF generated with a2ps → ${OUTPUT_FILE}"
    else
        echo "  a2ps method failed, trying alternatives..."
    fi
fi

if [ "$GENERATED" -eq 0 ]; then
    generate_text
fi

# ── Summary ──
echo ""
echo "═══════════════════════════════════════════"
echo " Done!"
echo " Codes: ${#CODES[@]}"
if [ "$GENERATED" -eq 1 ]; then
    echo " PDF:  ${OUTPUT_FILE}"
fi
echo "═══════════════════════════════════════════"
