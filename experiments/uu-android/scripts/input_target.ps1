param([Parameter(Mandatory=$true)][string]$Report)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$form = New-Object Windows.Forms.Form
$form.Text = 'DD UU remote-input test — synthetic text only'
$form.Size = New-Object Drawing.Size(900,650)
$form.StartPosition = 'CenterScreen'
$form.TopMost = $true
$box = New-Object Windows.Forms.TextBox
$box.Multiline = $true
$box.Dock = 'Fill'
$box.Font = New-Object Drawing.Font('Microsoft YaHei UI',22)
$form.Controls.Add($box)
$script:first = $false
$script:last = $false
$script:leftClicks = 0
$script:rightClicks = 0
$script:wheels = 0
$script:dragged = $false
$script:initial = $null
function Save-Report {
    @{firstChineseEmoji=$script:first;shortcutReplace=$script:last;leftClicks=$script:leftClicks;rightClicks=$script:rightClicks;wheelEvents=$script:wheels;dragged=$script:dragged} | ConvertTo-Json | Set-Content -LiteralPath $Report -Encoding utf8
}
$box.Add_TextChanged({
    if ($box.Text -eq '你好🙂 DD') { $script:first=$true }
    if ($box.Text -eq 'DD 输入验证🙂') { $script:last=$true }
    Save-Report
})
$box.Add_MouseDown({param($sender,$event)
    if ($event.Button -eq 'Left') { $script:leftClicks++;$script:initial=$event.Location }
    if ($event.Button -eq 'Right') { $script:rightClicks++ }
    Save-Report
})
$box.Add_MouseMove({param($sender,$event)
    if ($event.Button -eq 'Left' -and $script:initial -and [Math]::Abs($event.X-$script:initial.X) -gt 20) {$script:dragged=$true;Save-Report}
})
$box.Add_MouseWheel({$script:wheels++;Save-Report})
$form.Add_Shown({$form.Activate();$box.Focus();Save-Report})
$timer=New-Object Windows.Forms.Timer
$timer.Interval=180000
$timer.Add_Tick({$form.Close()})
$timer.Start()
$showTimer=New-Object Windows.Forms.Timer
$showTimer.Interval=1000
$showTimer.Add_Tick({$showTimer.Stop();$form.Show();$form.Activate();$box.Focus()})
$showTimer.Start()
[Windows.Forms.Application]::Run($form)
$timer.Dispose()
$form.Dispose()
