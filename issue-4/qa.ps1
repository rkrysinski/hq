# QA helper for driving a real Windows Terminal window from WSL.
#   qa.ps1 list                      windows with a title
#   qa.ps1 shot  TITLE OUT.png       picture of the window whose title contains TITLE
#   qa.ps1 taskbar OUT.png           picture of the taskbar strip
#   qa.ps1 keys  TITLE KEYS          bring the window to front, type KEYS (SendKeys syntax)
#   qa.ps1 fg                        title of the foreground window
#   qa.ps1 front TITLE               bring the window to front
#   qa.ps1 resize TITLE W H          resize the window (pixels)
#   qa.ps1 close TITLE               close the window
param([string]$cmd, [string]$a1, [string]$a2, [string]$a3)
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
Add-Type @"
using System; using System.Text; using System.Runtime.InteropServices;
public class W {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint f);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr h, int x, int y, int w, int hh, bool r);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc p, IntPtr l);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
}
"@
[void][W]::SetProcessDPIAware()

function Titles {
  $res = New-Object System.Collections.ArrayList
  $cb = [W+EnumProc]{ param($h, $l)
    if ([W]::IsWindowVisible($h)) {
      $sb = New-Object System.Text.StringBuilder 512
      [void][W]::GetWindowText($h, $sb, 512)
      if ($sb.Length -gt 0) { [void]$res.Add([pscustomobject]@{ Handle = $h; Title = $sb.ToString() }) }
    }
    return $true }
  [void][W]::EnumWindows($cb, [IntPtr]::Zero)
  $res
}
function Find($t) {
  $w = Titles | Where-Object { $_.Title -like "*$t*" } | Select-Object -First 1
  if (-not $w) { Write-Error "no window titled *$t*"; exit 2 }
  $w.Handle
}
function Front($h) {
  # An Alt tap lets a background process take the foreground (foreground lock).
  [W]::keybd_event(0x12, 0, 0, [UIntPtr]::Zero); [W]::keybd_event(0x12, 0, 2, [UIntPtr]::Zero)
  [void][W]::ShowWindow($h, 9); [void][W]::SetForegroundWindow($h); Start-Sleep -Milliseconds 400
}
function FgTitle { $sb = New-Object System.Text.StringBuilder 512; [void][W]::GetWindowText([W]::GetForegroundWindow(), $sb, 512); $sb.ToString() }

switch ($cmd) {
  'list' { Titles | ForEach-Object { $_.Title } }
  'fg' { FgTitle }
  'front' { Front (Find $a1); FgTitle }
  'shot' {
    $h = Find $a1; $r = New-Object W+RECT; [void][W]::GetWindowRect($h, [ref]$r)
    $bmp = New-Object System.Drawing.Bitmap ($r.R - $r.L), ($r.B - $r.T)
    $g = [System.Drawing.Graphics]::FromImage($bmp); $dc = $g.GetHdc()
    [void][W]::PrintWindow($h, $dc, 2); $g.ReleaseHdc($dc); $g.Dispose()
    $bmp.Save($a2, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose(); "$a2"
  }
  'taskbar' {
    $b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds; $wa = [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea
    $hgt = $b.Height - $wa.Height; if ($hgt -lt 20) { $hgt = 48 }
    $bmp = New-Object System.Drawing.Bitmap $b.Width, $hgt
    $g = [System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen(0, $b.Height - $hgt, 0, 0, $bmp.Size); $g.Dispose()
    $bmp.Save($a1, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose(); "$a1"
  }
  'keys' { Front (Find $a1); [System.Windows.Forms.SendKeys]::SendWait($a2); Start-Sleep -Milliseconds 300; FgTitle }
  'resize' { $h = Find $a1; $r = New-Object W+RECT; [void][W]::GetWindowRect($h, [ref]$r); [void][W]::MoveWindow($h, $r.L, $r.T, [int]$a2, [int]$a3, $true) }
  'min' { [void][W]::ShowWindow((Find $a1), 6) }
  'restore' { [void][W]::ShowWindow((Find $a1), 9) }
  'close' { [void][W]::SendMessage((Find $a1), 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) }
  default { Write-Error "unknown command $cmd"; exit 1 }
}
