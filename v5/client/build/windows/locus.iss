; Locus — Windows installer (Inno Setup 6)
;
; WHY THIS EXISTS
;
; The client used to ship only as a portable zip. Students ran locus.exe from
; wherever they extracted it — most often Downloads — and the auto-updater
; staged and renamed its download *in that same directory*. On Windows a fresh
; .exe in Downloads is opened within milliseconds by Defender, SmartScreen, the
; search indexer and sync clients, and a rename cannot succeed while any of them
; holds a handle:
;
;   download failed: cannot rename downloaded file: rename
;   C:\Users\Hello\Downloads\locus-windows-amd64.exe.new.tmp
;   C:\Users\Hello\Downloads\locus-windows-amd64.exe.new:
;   The process cannot access the file because it is being used by another process.
;
; Installing into a directory the application owns removes the whole class of
; problem: the staging area is private to Locus, and the install directory is
; not a folder that strangers watch. internal/install resolves this location at
; runtime, and {app} below is what makes it exist.
;
; The portable zip CONTINUES TO SHIP. This installer is an additional option,
; not a replacement — a student without admin rights, or running from a USB
; stick, keeps working exactly as before.
;
; Targets: Inno Setup 6 (iscc). Built in CI on windows-latest, where Inno Setup
; is preinstalled. PrivilegesRequired=admin is deliberate: the client already
; requires elevation at runtime (its embedded manifest sets
; requestedExecutionLevel="requireAdministrator" for the TUN adapter), so
; asking once at install time is not an extra burden, and it lets the updater
; write to Program Files without a second prompt.

#define AppName "Locus"
#define AppPublisher "Locus"
#define AppExeName "locus.exe"

; Version is passed in from CI (from v5/VERSION) so this file never holds a
; hardcoded number that can drift. v5/client/version_consistency_test.go exists
; precisely because hand-maintained versions in several files diverged before.
#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

; PATHS — WHY THESE ARE PASSED IN RATHER THAN WRITTEN RELATIVELY
;
; Inno Setup resolves a relative OutputDir against SourceDir, and SourceDir
; defaults to the directory holding this script, i.e. v5/client/build/windows.
; The [Files] Source parameters resolve the same way (the compiler "prepends
; the path of your installation's source directory" to a relative Source).
;
; CI, however, compiles this script from v5/client, where the freshly built
; locus-windows-amd64.exe and sing-box.exe actually live, and then looks for
; the result in v5/client/dist. Writing the paths relatively therefore works
; only by accident of which directory the compiler happens to be invoked from.
; It did not work: relative Source looked in build/windows/ (no binaries) and
; relative OutputDir would have written build/windows/dist (not where CI looks
; for *.exe). Passing both in as fully-qualified paths makes the script correct
; regardless of the invoking directory.
; BuildDir and OutputDirAbs are fully-qualified paths supplied by CI. The
; fallbacks exist so the script can still be compiled by hand from
; v5/client/build/windows (where the binaries are two levels up and dist/ is a
; sibling of this directory); they are plain directory names resolved the
; ordinary way, deliberately avoiding string-building in a #define.
#ifndef BuildDir
  #define BuildDir "..\\.."
#endif
#ifndef OutputDirAbs
  #define OutputDirAbs "..\\..\\dist"
#endif

[Setup]
AppId={{8E4C1F52-3A7B-4D9E-9C21-5F6A0D7B2E14}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
OutputDir={#OutputDirAbs}
OutputBaseFilename=locus-setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

; 64-bit only: the shipped binaries are amd64. A 32-bit install would create a
; copy that cannot run.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

; The client needs elevation at runtime for the TUN adapter (see the embedded
; manifest). Requiring it at install time means the updater can later write to
; {app} without prompting again.
PrivilegesRequired=admin

; An installed copy must not be running while it is replaced. Without this the
; installer fails with the same class of sharing violation the updater hit.
CloseApplications=yes

UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExeName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; The client and its engine. sing-box is bundled rather than downloaded because
; the whole reason the client exists is a network where downloads are blocked —
; a bootstrap download would fail exactly where the app is needed most.
Source: "{#BuildDir}\locus-windows-amd64.exe"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion
Source: "{#BuildDir}\sing-box.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"
Name: "{group}\{cm:UninstallProgram,{#AppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; The updater's private staging directory and its rollback copies. Left behind,
; they are a few megabytes of stale binaries in a directory the user believes
; they removed.
Type: filesandordirs; Name: "{app}\.locus-staging"
Type: filesandordirs; Name: "{app}\.locus-backups"
Type: files; Name: "{app}\.update-pending"
Type: files; Name: "{app}\.update-confirmed"

[UninstallRun]
; Windows does not release a running executable's file lock on uninstall, so a
; leftover engine or client would block removal outright. Best-effort kill with
; a taskkill that tolerates the processes already being gone (exit code 128).
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM sing-box.exe /T"; Flags: runhidden; RunOnceId: "KillEngine"
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM locus.exe /T"; Flags: runhidden; RunOnceId: "KillClient"
