$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'
$title = 'hq - agents'
$w = Add-Type -PassThru -Namespace Hq -Name Win -MemberDefinition '
[DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(IntPtr cls, string title);
[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
[DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
[DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
[DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
[DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);'
$h = $w::FindWindow([IntPtr]::Zero, $title)
if ($h -eq [IntPtr]::Zero) { [Console]::Error.WriteLine("no window titled $title"); exit 1 }
function InFront { Start-Sleep -Milliseconds 100; $w::GetForegroundWindow() -eq $h }
if ($w::IsIconic($h)) { [void]$w::ShowWindow($h, 9) }
[void]$w::SetForegroundWindow($h)
if (InFront) { exit 0 }
$w::keybd_event(0x12, 0, 0, [UIntPtr]::Zero); $w::keybd_event(0x12, 0, 2, [UIntPtr]::Zero)
[void]$w::SetForegroundWindow($h)
if (InFront) { exit 0 }
[Console]::Error.WriteLine("Windows kept another window in front; the window titled $title flashes on the taskbar")
exit 1
