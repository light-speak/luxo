param(
    [Parameter(Mandatory = $true)]
    [string] $ProjectRoot
)

$ErrorActionPreference = 'Stop'
if (-not $IsWindows) {
    throw 'Run this probe on native Windows with PowerShell 7.'
}
$project = (Resolve-Path -LiteralPath $ProjectRoot).Path
$scratch = Join-Path $project '.tmp'
[System.IO.Directory]::CreateDirectory($scratch) | Out-Null

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

public static class LuxoRenameAbiProbe {
    private const uint SourceAccess = 0x00010080;
    private const uint Synchronize = 0x00100000;
    private const uint DirectoryAccess = 0x001000a0;
    private const uint ShareAll = 7;
    private const uint OpenExisting = 3;
    private const uint BackupSemantics = 0x02000000;
    private const uint OpenReparsePoint = 0x00200000;
    private const int StatusPending = 0x00000103;
    private const int StatusCollision = unchecked((int)0xc0000035);

    // ABI fields are also read or written through native buffers.
#pragma warning disable 0649
    [StructLayout(LayoutKind.Sequential)]
    private struct RenameInfo {
        public uint Flags;
        public IntPtr RootDirectory;
        public uint FileNameLength;
        public ushort FileName;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct UnicodeString {
        public ushort Length;
        public ushort MaximumLength;
        public IntPtr Buffer;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct ObjectAttributes {
        public uint Length;
        public IntPtr RootDirectory;
        public IntPtr ObjectName;
        public uint Attributes;
        public IntPtr SecurityDescriptor;
        public IntPtr SecurityQualityOfService;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IoStatusBlock {
        public IntPtr Status;
        public UIntPtr Information;
    }
#pragma warning restore 0649

    [DllImport("kernel32.dll", ExactSpelling = true, CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern SafeFileHandle CreateFileW(
        string name, uint desiredAccess, uint shareMode, IntPtr security,
        uint creation, uint flags, IntPtr templateFile);

    [DllImport("kernel32.dll", ExactSpelling = true, SetLastError = true)]
    private static extern int SetFileInformationByHandle(
        SafeFileHandle file, int infoClass, IntPtr info, uint length);

    [DllImport("kernel32.dll", ExactSpelling = true, SetLastError = true)]
    private static extern uint WaitForSingleObject(SafeFileHandle handle, uint milliseconds);

    [DllImport("ntdll.dll", ExactSpelling = true)]
    private static extern int NtCreateFile(
        out SafeFileHandle file, uint desiredAccess, ref ObjectAttributes attributes,
        out IoStatusBlock iosb, IntPtr allocationSize, uint fileAttributes,
        uint shareAccess, uint disposition, uint options, IntPtr ea, uint eaLength);

    [DllImport("ntdll.dll", ExactSpelling = true)]
    private static extern uint RtlNtStatusToDosError(int status);

    public sealed class Result {
        public string Label { get; set; }
        public int Round { get; set; }
        public bool Collision { get; set; }
        public string Leaf { get; set; }
        public bool Success { get; set; }
        public bool DataVerified { get; set; }
        public bool CollisionStatusMatched { get; set; }
        public uint Win32Code { get; set; }
        public string NtReturn { get; set; }
        public string NtCompletion { get; set; }
        public int PointerSize { get; set; }
        public int RootOffset { get; set; }
        public int LengthOffset { get; set; }
        public int NameOffset { get; set; }
        public int StructureSize { get; set; }
        public int NameBytes { get; set; }
        public int BufferLength { get; set; }
    }

    private static int Offset(string field) {
        return Marshal.OffsetOf(typeof(RenameInfo), field).ToInt32();
    }

    private static SafeFileHandle Open(string path, uint access, uint flags) {
        SafeFileHandle file = CreateFileW(path, access, ShareAll, IntPtr.Zero, OpenExisting, flags, IntPtr.Zero);
        if (file.IsInvalid) {
            int error = Marshal.GetLastWin32Error();
            file.Dispose();
            throw new Win32Exception(error, "CreateFileW");
        }
        return file;
    }

    private static SafeFileHandle OpenNative(SafeFileHandle parent, string leaf, uint access, bool directory) {
        IntPtr nameBuffer = Marshal.StringToHGlobalUni(leaf);
        IntPtr name = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(UnicodeString)));
        try {
            ushort length = checked((ushort)(leaf.Length * 2));
            Marshal.StructureToPtr(new UnicodeString {
                Length = length, MaximumLength = checked((ushort)(length + 2)), Buffer = nameBuffer
            }, name, false);
            ObjectAttributes attributes = new ObjectAttributes {
                Length = (uint)Marshal.SizeOf(typeof(ObjectAttributes)),
                RootDirectory = parent.DangerousGetHandle(),
                ObjectName = name,
                Attributes = 0x40
            };
            SafeFileHandle file;
            IoStatusBlock iosb;
            int status = NtCreateFile(out file, access, ref attributes, out iosb,
                IntPtr.Zero, 0, ShareAll, 1, 0x20 | (directory ? 1u : 0x40u | OpenReparsePoint), IntPtr.Zero, 0);
            GC.KeepAlive(parent);
            if (status != 0 || file == null || file.IsInvalid) {
                if (file != null) file.Dispose();
                throw new Win32Exception((int)RtlNtStatusToDosError(status),
                    "NtCreateFile NTSTATUS " + Hex(status));
            }
            return file;
        } finally {
            Marshal.FreeHGlobal(name);
            Marshal.FreeHGlobal(nameBuffer);
        }
    }

    private static string Hex(int status) {
        return "0x" + unchecked((uint)status).ToString("x8");
    }

    private static Result Rename(SafeFileHandle source, SafeFileHandle target, string name,
        string label, bool nt, bool padded, bool terminated, bool collision) {
        byte[] nameBytes = Encoding.Unicode.GetBytes(name);
        int offset = Offset("FileName");
        int structSize = Marshal.SizeOf(typeof(RenameInfo));
        int length = padded ? structSize + nameBytes.Length + 2 : offset + nameBytes.Length + (terminated ? 2 : 0);
        byte[] bytes = new byte[Math.Max(length, structSize)];
        IntPtr buffer = Marshal.AllocHGlobal(bytes.Length);
        IntPtr iosb = Marshal.AllocHGlobal(IntPtr.Size * 2);
        try {
            Marshal.Copy(bytes, 0, buffer, bytes.Length);
            Marshal.WriteIntPtr(buffer, Offset("RootDirectory"), target.DangerousGetHandle());
            Marshal.WriteInt32(buffer, Offset("FileNameLength"), nameBytes.Length);
            Marshal.Copy(nameBytes, 0, IntPtr.Add(buffer, offset), nameBytes.Length);
            Marshal.WriteIntPtr(iosb, 0, IntPtr.Zero);
            Marshal.WriteIntPtr(iosb, IntPtr.Size, IntPtr.Zero);
            var result = new Result {
                Label = label, Collision = collision, Leaf = name, PointerSize = IntPtr.Size, RootOffset = Offset("RootDirectory"),
                LengthOffset = Offset("FileNameLength"), NameOffset = offset, StructureSize = structSize,
                NameBytes = nameBytes.Length, BufferLength = length
            };
            if (!nt) {
                int success = SetFileInformationByHandle(source, 3, buffer, (uint)length);
                result.Win32Code = success == 0 ? (uint)Marshal.GetLastWin32Error() : 0;
                result.Success = success != 0;
            } else {
                int status = SetNative(source, iosb, buffer, (uint)length);
                result.NtReturn = Hex(status);
                if (status == StatusPending) {
                    // The source was explicitly opened with SYNCHRONIZE. Keep both buffers
                    // alive until completion and let the hosted job deadline bound the probe.
                    if (WaitForSingleObject(source, uint.MaxValue) != 0)
                        Environment.FailFast("Waiting for pending native rename failed.");
                    status = Marshal.ReadInt32(iosb);
                }
                result.NtCompletion = Hex(status);
                result.Win32Code = status == 0 ? 0 : RtlNtStatusToDosError(status);
                result.Success = status == 0;
            }
            GC.KeepAlive(target);
            return result;
        } finally {
            Marshal.FreeHGlobal(iosb);
            Marshal.FreeHGlobal(buffer);
        }
    }

    [DllImport("ntdll.dll", EntryPoint = "NtSetInformationFile", ExactSpelling = true)]
    private static extern int SetNative(SafeFileHandle file, IntPtr iosb, IntPtr info, uint length, int infoClass);

    private static int SetNative(SafeFileHandle file, IntPtr iosb, IntPtr info, uint length) {
        return SetNative(file, iosb, info, length, 10);
    }

    public static Result Run(string workspace, string label, bool nt, bool nativeSource, bool nativeTarget,
        bool padded, bool terminated, bool synchronizedAccess, bool addFileAccess, bool collision, string leaf) {
        string fixture = Path.Combine(workspace, "rename-abi-" + Guid.NewGuid().ToString("N"));
        string sourceDir = Path.Combine(fixture, "staging");
        string targetDir = Path.Combine(fixture, "output");
        const string input = "staged.txt";
        string sourceFile = Path.Combine(sourceDir, input);
        string targetFile = Path.Combine(targetDir, leaf);
        const string expected = "prepared-artifact";
        const string sentinel = "existing-output";
        try {
            Directory.CreateDirectory(sourceDir);
            Directory.CreateDirectory(targetDir);
            File.WriteAllText(sourceFile, expected, Encoding.UTF8);
            if (collision) File.WriteAllText(targetFile, sentinel, Encoding.UTF8);
            uint desiredSource = SourceAccess | (synchronizedAccess ? Synchronize : 0);
            uint desiredTarget = DirectoryAccess | (addFileAccess ? 2u : 0u);
            Result result;
            using (SafeFileHandle sourceFolder = nativeSource ? Open(sourceDir, DirectoryAccess, BackupSemantics) : null)
            using (SafeFileHandle source = nativeSource ?
                OpenNative(sourceFolder, input, desiredSource, false) :
                Open(sourceFile, desiredSource, BackupSemantics | OpenReparsePoint))
            using (SafeFileHandle targetAncestor = nativeTarget ? Open(fixture, DirectoryAccess, BackupSemantics) : null)
            using (SafeFileHandle target = nativeTarget ?
                OpenNative(targetAncestor, "output", desiredTarget, true) :
                Open(targetDir, desiredTarget, BackupSemantics)) {
                result = Rename(source, target, leaf, label, nt, padded, terminated, collision);
            }
            result.CollisionStatusMatched = result.Win32Code == 80 || result.Win32Code == 183 ||
                result.NtCompletion == Hex(StatusCollision);
            if (collision) {
                result.DataVerified = !result.Success && File.ReadAllText(targetFile) == sentinel &&
                    File.ReadAllText(sourceFile) == expected;
            } else if (result.Success) {
                result.DataVerified = !File.Exists(sourceFile) && File.ReadAllText(targetFile) == expected;
            } else {
                result.DataVerified = !File.Exists(targetFile) && File.ReadAllText(sourceFile) == expected;
            }
            if (!result.DataVerified) throw new IOException("Unexpected filesystem state: " + label);
            return result;
        } finally {
            if (Directory.Exists(fixture)) Directory.Delete(fixture, true);
        }
    }
}
'@

$cases = @(
    @{ Name = 'win32-current';      Nt = $false; Native = $false; Padded = $false; Terminated = $true;  Sync = $false; AddFile = $false },
    @{ Name = 'win32-size-padded';  Nt = $false; Native = $false; Padded = $true;  Terminated = $true;  Sync = $false; AddFile = $false },
    @{ Name = 'win32-size-counted'; Nt = $false; Native = $false; Padded = $false; Terminated = $false; Sync = $false; AddFile = $false },
    @{ Name = 'win32-source-sync';  Nt = $false; Native = $false; Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $false },
    @{ Name = 'nt-win32-open';      Nt = $true;  Native = $false; Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $false },
    @{ Name = 'nt-native-open';     Nt = $true;  Native = $true;  Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $false },
    @{ Name = 'nt-native-compact';  Nt = $true;  Native = $true;  Padded = $false; Terminated = $false; Sync = $true;  AddFile = $false },
    @{ Name = 'nt-target-add-file'; Nt = $true;  Native = $true;  Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $true  },
    @{ Name = 'win32-nt-target';    Nt = $false; Native = $false; Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $false; NativeTarget = $true },
    @{ Name = 'nt-nt-target';       Nt = $true;  Native = $true;  Padded = $true;  Terminated = $true;  Sync = $true;  AddFile = $false; NativeTarget = $true }
)

$roundCount = 2
$leaves = @('result.exe', 'result-用-𠀀.exe')
$observations = @(for ($round = 1; $round -le $roundCount; $round++) {
    foreach ($case in $cases) {
        foreach ($leaf in $leaves) {
            foreach ($existing in @($false, $true)) {
                try {
                    $result = [LuxoRenameAbiProbe]::Run($scratch, $case.Name, $case.Nt, $case.Native, [bool]$case.NativeTarget,
                        $case.Padded, $case.Terminated, $case.Sync, $case.AddFile, $existing, $leaf)
                    $result.Round = $round
                    $result
                } catch {
                    [pscustomobject]@{
                        Label = $case.Name
                        Round = $round
                        Leaf = $leaf
                        Collision = $existing
                        DataVerified = $false
                        DiagnosticError = $_.Exception.GetBaseException().Message
                    }
                }
            }
        }
    }
})

$expectedRows = $roundCount * $cases.Count * $leaves.Count * 2
$groups = @($observations | Group-Object -Property Label, Leaf, Collision)
$unstable = @($groups | Where-Object {
    $outcomes = @($_.Group | ForEach-Object {
        @($_.Success, $_.DataVerified, $_.CollisionStatusMatched, $_.Win32Code, $_.NtReturn, $_.NtCompletion,
            $_.NameBytes, $_.BufferLength, $_.DiagnosticError) -join '|'
    } | Sort-Object -Unique)
    $_.Count -ne $roundCount -or $outcomes.Count -ne 1
})
$unverified = @($observations | Where-Object { -not $_.DataVerified })
$qualified = @(foreach ($case in $cases) {
    $rows = @($observations | Where-Object { $_.Label -eq $case.Name })
    $mismatches = @($rows | Where-Object {
        -not $_.DataVerified -or
            ($_.Collision -and ($_.Success -or -not $_.CollisionStatusMatched)) -or
            (-not $_.Collision -and -not $_.Success)
    })
    if ($rows.Count -eq ($roundCount * $leaves.Count * 2) -and $mismatches.Count -eq 0) {
        $case.Name
    }
})
$original87 = @($observations | Where-Object {
    $_.Label -eq 'win32-current' -and -not $_.Collision -and $_.Win32Code -eq 87
}).Count

$verdict = if ($observations.Count -ne $expectedRows -or $groups.Count -ne ($expectedRows / $roundCount)) {
    'incomplete-observations'
} elseif ($unverified.Count -ne 0) {
    'unverified-filesystem-state'
} elseif ($unstable.Count -ne 0) {
    'unstable-observations'
} elseif ($qualified.Count -ne 0) {
    'rooted-candidate-observed'
} else {
    'no-rooted-candidate-observed'
}

[ordered]@{
    Os = [System.Environment]::OSVersion.VersionString
    Architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    Filesystem = [System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot($scratch)).DriveFormat
    Verdict = $verdict
    ExpectedRows = $expectedRows
    OriginalWin32FreshError87 = $original87
    CandidateLabels = $qualified
    UnverifiedRows = $unverified.Count
    UnstableGroups = @($unstable | ForEach-Object { $_.Name })
    Rows = $observations
} | ConvertTo-Json -Depth 5

Write-Information ("verdict={0} observations={1}/{2} original-fresh-error-87={3} candidates={4}" -f
    $verdict, $observations.Count, $expectedRows, $original87, ($qualified -join ',')) -InformationAction Continue
if ($verdict -notin @('rooted-candidate-observed', 'no-rooted-candidate-observed')) {
    throw "Invalid native rename evidence ($verdict)."
}
