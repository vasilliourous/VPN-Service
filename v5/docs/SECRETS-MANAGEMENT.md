# MyVPN Secrets Management

> How credentials are securely deployed with the server — encrypted in the repo,
> decrypted at deploy time.

---

## Problem

The MyVPN server needs several credentials to run:

| Credential | Used by |
|---|---|
| `DOMAIN` | Every module — the VPS hostname |
| `ADMIN_API_TOKEN` | Admin unbind endpoint + code generator |
| `B2_APPLICATION_KEY_ID` | Backups module (Backblaze B2 auth) |
| `B2_APPLICATION_KEY` | Backups module (Backblaze B2 auth) |
| `B2_BUCKET` | Backups module (bucket name) |
| `ECO_PASS`, `STEALTH_PASS`, `STRIKE_PASS` | Shadowsocks tier passwords |
| `PB_ADMIN_EMAIL`, `PB_ADMIN_PASS` | PocketBase admin credentials |

These must be available at deploy time, but **should not be committed in plain-text**
to the repository. At the same time, one-command deploy is important — nobody wants
to type 7 environment variables every time they provision a VPS.

---

## Solution: Age-Encrypted Secrets File

**[age](https://age-encryption.org/)** is a modern file encryption tool (Go, single binary).
We encrypt a `.env` file with all credentials and commit the encrypted version.
At deploy time, `setup.sh` automatically decrypts it using a key that is **not** in the repo.

```
v5/server/
├── secrets.env.age      ← Encrypted. Committed to git. Safe to share.
├── age-key.txt          ← PLAINTEXT KEY. NEVER commit. .gitignored.
├── age-key.txt.pub      ← Public key. Can be committed.
├── setup.sh             ← Decrypts secrets.env.age on start.
└── restore.sh           ← Same.
```

### Threat model

| Attack vector | Mitigation |
|---|---|
| Repo leak (attacker gets source) | Secrets are encrypted — useless without the key |
| VPS compromise (attacker gets root) | Credentials are on-disk as env vars — acceptable (root has access anyway) |
| Deploy pipeline leak | Key is in CI secret store, never in logs |
| Lost key | Public key is in the repo — anyone can encrypt new secrets if they have the private key |

---

## One-Time Setup

### Step 1: Install age

```bash
# Linux (or any platform)
curl -sLO "https://github.com/FiloSottile/age/releases/download/v1.2.1/age-v1.2.1-linux-amd64.tar.gz"
tar -xzf age-v1.2.1-linux-amd64.tar.gz
sudo cp age/age age/age-keygen /usr/local/bin/
rm -rf age age-v1.2.1-linux-amd64.tar.gz

# Verify
age --version  # Should print 1.2.1
```

### Step 2: Generate a key pair

```bash
# Generate the private key
age-keygen -o age-key.txt
# Output: Public key: age1abc3def4ghj5klm6nop7qrs8tuv9wxyz0a1b2c3d4e5f6g7h8i9j0k1l2m3

# The private key is in age-key.txt — KEEP THIS SECRET
# Derive the public key (safe to share):
age-keygen -y age-key.txt
# Output: age1abc3def4ghj5klm6nop7qrs8tuv9wxyz0a1b2c3d4e5f6g7h8i9j0k1l2m3

# Save the public key for others who need to encrypt secrets:
age-keygen -y age-key.txt > age-key.txt.pub
```

**Protect the private key.** If someone has `age-key.txt`, they can decrypt all your
secrets. Store it in:
- Your password manager (1Password/Bitwarden)
- A USB drive in a safe
- GitHub Actions secret (for CI/CD)

### Step 3: Create the plain-text secrets file

```bash
# Create .secrets.env — this will be encrypted and then deleted
# Use your actual values — never commit this file
cat > .secrets.env << 'EOF'
# MyVPN Production Secrets
DOMAIN=networkingguides.duckdns.org
ADMIN_API_TOKEN=CslWcWOt7jFhmYELTZahvpqKF3uV/RnWChUYTjbVAU4=
B2_APPLICATION_KEY_ID=your-backblaze-key-id
B2_APPLICATION_KEY=your-backblaze-application-key
B2_BUCKET=vpsvpnbackup
ECO_PASS=your-64-char-hex-eco-password
STEALTH_PASS=your-64-char-hex-stealth-password
STRIKE_PASS=your-64-char-hex-strike-password
PB_ADMIN_EMAIL=admin@networkingguides.duckdns.org
PB_ADMIN_PASS=your-secure-pb-admin-password
EOF

# Set restrictive permissions
chmod 600 .secrets.env
```

> **Important:** Pre-generate stable tier passwords (`ECO_PASS`, `STEALTH_PASS`,
> `STRIKE_PASS`) and the PB admin password. Unlike the auto-generated approach,
> stable passwords mean a redeploy to a new VPS produces the **exact same config** —
> existing clients don't break, and backup restore keeps working.
>
> To generate tier passwords: `openssl rand -hex 16` (produces 32-char hex strings)
> To generate a PB admin password: `openssl rand -base64 24`

### Step 4: Encrypt the secrets file

```bash
# Encrypt using the PUBLIC key (you never need the private key for encryption)
age -r "$(cat age-key.txt.pub)" -o secrets.env.age .secrets.env

# Verify it worked
file secrets.env.age
# Output: secrets.env.age: age file, version 1

# Securely delete the plain-text file
shred -u .secrets.env
```

### Step 5: Commit the encrypted file

```bash
# The encrypted file is safe to commit
git add v5/server/secrets.env.age

# NEVER commit the private key or plain-text file
echo "age-key.txt" >> v5/server/.gitignore
echo ".secrets.env" >> v5/server/.gitignore

git commit -m "Add encrypted production secrets"
```

---

## How Decryption Works at Deploy Time

Both `setup.sh` and `restore.sh` have the same decryption logic at the top.
They try three key sources in order:

### Source 1: `AGE_KEY` environment variable (CI/CD)

```bash
# Pass the key content directly (GitHub Actions secret, etc.)
AGE_KEY="AGE-SECRET-KEY-1..." ssh root@vps 'bash -s' < v5/server/setup.sh
```

Best for CI/CD pipelines. The key is stored as a secret in GitHub Actions /
GitLab CI / etc.

### Source 2: `age-key.txt` file (local deploy)

```bash
# Place the key file alongside the scripts
scp age-key.txt root@vps:/root/server/
ssh root@vps "DOMAIN=... /root/server/setup.sh"
```

Best for deploying from your local machine. The key file is in the same
directory as `setup.sh` — decryption happens automatically.

### Source 3: Fail with instructions

If neither source provides a key, the script exits with a clear message
telling the operator how to provide one.

---

## Deploy Commands (Now One-Line)

### From local machine (key file present)

```bash
# Copy server code + key to VPS
scp -r v5/server age-key.txt root@vps:/root/server/

# One-command deploy — no env vars needed
ssh root@vps "/root/server/setup.sh"
```

### From local machine (key in ssh agent or pass)

```bash
# Pipe the script — AGE_KEY comes from your local file
AGE_KEY=$(cat age-key.txt) ssh root@vps 'bash -s' < v5/server/setup.sh
```

### From CI/CD (GitHub Actions)

```yaml
- name: Deploy to VPS
  env:
    AGE_KEY: ${{ secrets.MYVPN_AGE_KEY }}
  run: |
    echo "$AGE_KEY" | ssh root@vps 'bash -s' < v5/server/setup.sh
```

### Disaster recovery (restore.sh)

```bash
# Same three options — decrypts B2 creds automatically
AGE_KEY=$(cat age-key.txt) \
  ssh root@new-vps 'bash -s' < v5/server/restore.sh

# Or with key file on VPS:
scp age-key.txt root@new-vps:/root/server/
ssh root@new-vps "/root/server/restore.sh"
```

---

## Updating Secrets

When a credential changes (e.g., B2 application key rotated):

```bash
# 1. Decrypt the existing file
age -d -i age-key.txt secrets.env.age > .secrets.env

# 2. Edit the values
vim .secrets.env

# 3. Re-encrypt
age -r "$(cat age-key.txt.pub)" -o secrets.env.age .secrets.env

# 4. Delete plain-text
shred -u .secrets.env

# 5. Commit
git add secrets.env.age
git commit -m "Update B2 application key"
```

---

## Key Rotation

If the age key is compromised:

```bash
# 1. Generate a new key pair
age-keygen -o age-key-new.txt

# 2. Re-encrypt secrets with the new public key
age -r "$(age-keygen -y age-key-new.txt)" -o secrets.env.age .secrets.env

# 3. Replace old key
mv age-key-new.txt age-key.txt
age-keygen -y age-key.txt > age-key.txt.pub

# 4. Commit the re-encrypted secrets
git add secrets.env.age
git commit -m "Rotate age encryption key"

# 5. Update key in CI secrets / password manager
```

---

## FAQ

### Can I use a passphrase instead of a key file?

Yes. Age supports passphrase-based encryption:

```bash
# Encrypt with passphrase
age -p -o secrets.env.age .secrets.env

# Decrypt (prompts for passphrase)
source <(age -d secrets.env.age)
```

However, this removes the benefit of unattended deployment. Use a key file
for automated deploys.

### What if I lose the private key?

Anyone with the **public key** can encrypt new secrets, but only the holder of the
**private key** can decrypt. If you lose the private key:
1. Generate a new key pair
2. Create a fresh `.secrets.env` with all new passwords (old clients will break)
3. Encrypt and deploy

Keep the private key in a password manager to avoid this.

### Why not SOPS / Vault / git-crypt?

| Tool | Issue for this project |
|---|---|
| **SOPS** | Powerful but adds YAML/JSON schema overhead. We just need to `source` env vars. |
| **HashiCorp Vault** | Requires a server, complex ACLs, token management. Overkill for one VPS. |
| **git-crypt** | Requires GPG, git filter setup. Breaks if someone clones without it. |
| **GPG directly** | Terrible UX. Key management is painful. Age is strictly simpler. |
| **Docker secrets** | You're not using Docker. |

Age is the simplest tool that does exactly what we need: encrypt a file such
that `bash` can `source` the decrypted output in one line.

### Why pre-generate tier passwords instead of auto-generating?

The current setup auto-generates `ECO_PASS`, `STEALTH_PASS`, `STRIKE_PASS` on
each deploy via `02-shadowsocks.sh`. This means:
- Redeploying to a new VPS produces **different passwords**
- All existing clients would need to re-activate
- Backup restore from a different deploy would have mismatched passwords

By putting **stable, pre-generated** passwords in the encrypted secrets file,
you ensure that every deploy produces the exact same config — existing clients
keep working, and disaster recovery just works.

To pre-generate:
```bash
openssl rand -hex 16   # → 32-char hex string for each tier
```
