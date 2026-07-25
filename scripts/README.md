# MyVPN Scripts

Utility scripts for MyVPN code generation, printing, and operations.

## Files

| Script | Purpose |
|--------|---------|
| `generate_codes.sh` | Generate Luhn-mod-N activation codes and import to PocketBase |
| `print_codes.sh`    | Format codes into printable PDF card sheets |

## Quick Start

### Generate Codes

```bash
# Generate 50 Eco codes and import to PocketBase
./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50

# Dry run (no import)
DRY_RUN=1 ./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 50

# Custom expiry (30 days)
EXPIRY_DAYS=30 ./scripts/generate_codes.sh https://api.yourdomain.com YOUR_ADMIN_TOKEN eco 10
```

### Print Code Cards

```bash
# Generate PDF from codes file
./scripts/print_codes.sh eco-codes.txt eco-cards.pdf

# Pipe codes directly
cat eco-codes.txt | ./scripts/print_codes.sh -o eco-cards.pdf
```

## Code Format

```
MYVPN-XXXX-XXXX-XXXX-C
```

- **MYVPN**: Static prefix
- **XXXX**: Random base-32 segments (charset: `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, no I/O/0/1)
- **C**: Luhn-mod-N checksum character

The Luhn-mod-N checksum is validated client-side before sending to server,
preventing mistakes (typos) and reducing server load from invalid codes.

## Dependencies

- **generate_codes.sh**: bash, curl, python3 (for API import, optional)
- **print_codes.sh**: enscript + ghostscript (for PDF), or pandoc + wkhtmltopdf
