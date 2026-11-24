# 在本机（Windows 开发机）临时运行网站，给别人演示用。正式部署到 171 用 build.sh + install.sh。
#   启动 / 重启：powershell -ExecutionPolicy Bypass -File scripts\run-local.ps1
#   停止：      powershell -ExecutionPolicy Bypass -File scripts\run-local.ps1 -Stop
# 程序通过 WMI 在后台运行，关掉终端或 Claude 会话也不会停；电脑重启后要重新执行。
# 先用 go build -o bin\kx-opc-web.exe .\cmd\kx-opc-web 编译（前端要先 npm run build）。
param([switch]$Stop)

$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'bin\kx-opc-web.exe'
$config = Join-Path $root 'configs\config.yaml'

# 停掉已经在跑的（同一时间只能有一个实例，否则告警会记两遍）
Get-Process -Name 'kx-opc-web', 'kxweb' -ErrorAction SilentlyContinue | ForEach-Object {
    Write-Output "停止旧进程 $($_.Id)"
    Stop-Process -Id $_.Id -Force
}
if ($Stop) { return }

if (-not (Test-Path $exe)) { throw "找不到 $exe，请先编译" }

# 本机设了代理（127.0.0.1:7897），连 TD 的 WebSocket 不能走代理，否则报 unexpected EOF
$noProxy = 'localhost,127.0.0.1,::1,.local,10.65.104.171'

# 通过 WMI 启动：这样网站不是当前终端 / Claude 会话的子进程，关掉它们（或 Claude 重启）时不会被一起结束。
# 用 cmd 包一层是为了给网站设置 NO_PROXY（WMI 启动的进程不继承当前窗口的环境变量）。
$cmd = "cmd.exe /c set NO_PROXY=$noProxy&& set no_proxy=$noProxy&& `"$exe`" -config `"$config`""
$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
    CommandLine      = $cmd
    CurrentDirectory = $root
    ProcessStartupInformation = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
}
if ($r.ReturnValue -ne 0) { throw "启动失败，WMI 返回 $($r.ReturnValue)" }
Start-Sleep -Seconds 4
$p = Get-Process -Name 'kx-opc-web' -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $p) { throw "启动失败，看日志：$(Join-Path $root 'configs\logs\kx-opc-web.log')" }
Write-Output "已启动，进程 $($p.Id)，端口 8700"
Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } |
    ForEach-Object { Write-Output "  http://$($_.IPAddress):8700  （$($_.InterfaceAlias)）" }
