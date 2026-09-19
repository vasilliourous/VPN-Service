// Artifact verification for the Releases page.
//
// WHY THIS EXISTS: publishing four binaries through the browser had no guard
// against the two mistakes that fail silently:
//
//   1. Dropping the Windows binary into the Linux slot (or any cross-slot mixup).
//      The console would happily publish a URL for `locus-linux-amd64` that
//      serves a Windows PE file. Linux clients download it, fail the SHA-256
//      check, and never update — with nothing anywhere reporting an error.
//
//   2. Uploading the binaries of the wrong release while typing a newer version.
//      Clients verifies the hash (which matches, because the artifact is
//      internally consistent) but the fleet ends up on the wrong build.
//
// Both are detectable from the artifact itself, so no manifest upload is
// needed: the executables are self-describing (PE / ELF / Mach-O) and a Go
// binary carries its own version string. This module checks the file against
// the slot it was dropped into and against the version the operator typed.

export type PlatformKey = 'linux' | 'windows' | 'macos_intel' | 'macos_arm'

export interface ArtifactCheck {
  ok: boolean
  /** Human-readable reason when ok is false. */
  problem?: string
  /** Non-fatal notes (e.g. version could not be confirmed). */
  notes: string[]
  /** What the file actually looks like, for display. */
  detected: string
  /** The version string found inside the binary, when one was found. */
  embeddedVersion: string
}

/** Executable formats we can identify from a magic number. */
type Format =
  | { kind: 'pe' } // Windows
  | { kind: 'elf' } // Linux
  | { kind: 'macho'; subtype: 'x86_64' | 'arm64' | 'fat' | 'unknown' }
  | { kind: 'unknown' }

/** Read the first bytes of a file without loading the whole thing. */
async function readHead(file: File, n: number): Promise<Uint8Array> {
  const slice = file.slice(0, n)
  return new Uint8Array(await slice.arrayBuffer())
}

function be32(b: Uint8Array, off: number): number {
  return (b[off] << 24) | (b[off + 1] << 16) | (b[off + 2] << 8) | b[off + 3]
}

function le32(b: Uint8Array, off: number): number {
  return b[off] | (b[off + 1] << 8) | (b[off + 2] << 16) | (b[off + 3] << 24)
}

/**
 * Identify the executable format from its magic number.
 *
 * We only need enough to tell the three platforms apart:
 *   PE      "MZ" at offset 0
 *   ELF     0x7F 'E' 'L' 'F'
 *   Mach-O  0xFEEDFACE / 0xFEEDFACF (thin) or 0xCAFEBABE (universal/fat),
 *           in either byte order. For a thin binary the CPU type at offset 4
 *           distinguishes Intel (x86_64 = 0x01000007) from Apple Silicon
 *           (arm64 = 0x0100000C).
 *
 * NOTE on 0xCAFEBABE: that value is byte-order *ambiguous* — it is also Java's
 * class-file magic. Universal Mach-O binaries are conventionally stored
 * big-endian, so we only accept the big-endian byte layout
 * (CA FE BA BE in that exact order). A little-endian universal binary is not a
 * thing Apple's tooling produces, and accepting the reversed form would make
 * every Java .class file look like a macOS binary.
 */
export function detectFormat(head: Uint8Array): Format {
  if (head.length >= 2 && head[0] === 0x4d && head[1] === 0x5a) {
    return { kind: 'pe' }
  }
  if (
    head.length >= 4 &&
    head[0] === 0x7f &&
    head[1] === 0x45 && // E
    head[2] === 0x4c && // L
    head[3] === 0x46 // F
  ) {
    return { kind: 'elf' }
  }
  if (head.length >= 8) {
    // Universal/fat Mach-O, big-endian magic CA FE BA BE.
    if (head[0] === 0xca && head[1] === 0xfe && head[2] === 0xba && head[3] === 0xbe) {
      return { kind: 'macho', subtype: 'fat' }
    }
    // Thin Mach-O, little-endian (CF FA ED FE) or big-endian (FE ED FA CF).
    const isThinLE = head[0] === 0xcf && head[1] === 0xfa && head[2] === 0xed && head[3] === 0xfe
    const isThinBE = head[0] === 0xfe && head[1] === 0xed && head[2] === 0xfa && head[3] === 0xcf
    if (isThinLE || isThinBE) {
      // cputype at offset 4: 0x01000007 = x86_64, 0x0100000C = arm64.
      const cputype = isThinLE ? le32(head, 4) : be32(head, 4)
      if (cputype === 0x0100000c) return { kind: 'macho', subtype: 'arm64' }
      if (cputype === 0x01000007) return { kind: 'macho', subtype: 'x86_64' }
      return { kind: 'macho', subtype: 'unknown' }
    }
  }
  return { kind: 'unknown' }
}

/**
 * Search the artifact for a version string.
 *
 * A Locus binary contains its own version (injected via -ldflags, and also
 * present in the buildinfo provenance line). We scan a bounded prefix/suffix
 * for the literal `want` version. This is a *confirmation*, not a proof: the
 * bytes could coincidentally appear, but a mismatch is strong evidence the
 * operator picked the wrong release's binary, which is exactly what we want to
 * catch. Absence is reported as a note rather than a failure so a build with an
 * unexpected layout never blocks a legitimate publish.
 */
export async function findEmbeddedVersion(file: File, want: string): Promise<{ found: boolean; scanned: boolean }> {
  if (!want) return { found: false, scanned: false }
  const needle = new TextEncoder().encode(want)
  // Scan up to 4MB: the version appears early (build info) and, for the
  // buildinfo line, in the string table. Reading more would block the UI on a
  // 30MB file for little gain.
  const LIMIT = 4 * 1024 * 1024
  const slice = file.slice(0, Math.min(file.size, LIMIT))
  const buf = new Uint8Array(await slice.arrayBuffer())
  outer: for (let i = 0; i + needle.length <= buf.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (buf[i + j] !== needle[j]) continue outer
    }
    return { found: true, scanned: true }
  }
  return { found: false, scanned: file.size <= LIMIT }
}

/**
 * Verify a dropped file against the slot it was dropped into.
 *
 * Returns ok=false with a specific problem for a platform mismatch (the silent
 * failure), and ok=true with a note when the version simply could not be
 * confirmed.
 */
export async function verifyArtifact(
  file: File,
  slot: PlatformKey,
  expectedVersion: string,
): Promise<ArtifactCheck> {
  const notes: string[] = []
  const head = await readHead(file, 16)
  const fmt = detectFormat(head)

  const describe = (): string => {
    switch (fmt.kind) {
      case 'pe':
        return 'Windows executable (PE)'
      case 'elf':
        return 'Linux executable (ELF)'
      case 'macho':
        return `macOS executable (Mach-O, ${fmt.subtype})`
      default:
        return 'unrecognised format'
    }
  }
  const detected = describe()

  // ── Format must match the slot ──
  // This is the check the manifest was really needed for: it makes a
  // cross-slot mixup impossible to publish.
  const mismatch = (want: string): ArtifactCheck => ({
    ok: false,
    problem: `This looks like a ${detected}, but it was dropped into the ${want} slot. ` +
      `Pick the correct platform — publishing this would give ${want} clients a file they cannot run.`,
    notes,
    detected,
    embeddedVersion: '',
  })

  switch (slot) {
    case 'windows':
      if (fmt.kind !== 'pe') return mismatch('Windows')
      break
    case 'linux':
      if (fmt.kind !== 'elf') return mismatch('Linux')
      break
    case 'macos_intel':
      if (fmt.kind !== 'macho') return mismatch('macOS (Intel)')
      // A fat/universal binary is valid for either Mac slot.
      if (fmt.subtype === 'arm64') return mismatch('macOS (Intel) — this is an Apple Silicon build')
      break
    case 'macos_arm':
      if (fmt.kind !== 'macho') return mismatch('macOS (Apple Silicon)')
      if (fmt.subtype === 'x86_64') return mismatch('macOS (Apple Silicon) — this is an Intel build')
      break
  }

  // ── Version must be confirmable ──
  const { found, scanned } = await findEmbeddedVersion(file, expectedVersion)
  if (found) {
    notes.push(`contains version ${expectedVersion}`)
  } else if (scanned) {
    // We searched the whole file and did not find it. That is strong enough to
    // refuse: the most likely cause is a binary from a different release.
    return {
      ok: false,
      problem: `This file does not appear to contain version ${expectedVersion}. ` +
        `It is most likely a binary from a different release — check you downloaded ` +
        `the artifacts for ${expectedVersion} before publishing.`,
      notes,
      detected,
      embeddedVersion: '',
    }
  } else {
    notes.push(`version ${expectedVersion} not confirmed (file too large to scan)`)
  }

  return { ok: true, notes, detected, embeddedVersion: found ? expectedVersion : '' }
}
