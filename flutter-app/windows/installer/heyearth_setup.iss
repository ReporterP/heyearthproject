; HeYearth Windows Installer Script (Inno Setup)
; Requires Inno Setup 6.x: https://jrsoftware.org/isinfo.php

#define MyAppName "HeYearth"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "HeYearth"
#define MyAppURL "https://heyearth.ru"
#define MyAppExeName "heyearth_vpn.exe"

[Setup]
AppId={{A3F7E8C9-1234-5678-9ABC-DEF012345678}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
LicenseFile=
OutputDir=..\..\releases
OutputBaseFilename=HeyearthSetup-{#MyAppVersion}
SetupIconFile=..\..\assets\app_icon.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "autostart"; Description: "Launch HeYearth on Windows startup"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
; Main application files (Flutter build output)
Source: "..\..\build\windows\x64\runner\Release\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

; Xray-core binary
Source: "xray\xray.exe"; DestDir: "{app}\xray"; Flags: ignoreversion
Source: "xray\geoip.dat"; DestDir: "{app}\xray"; Flags: ignoreversion skipifsourcedoesntexist
Source: "xray\geosite.dat"; DestDir: "{app}\xray"; Flags: ignoreversion skipifsourcedoesntexist

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
; Auto-start on login (if task selected)
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "HeYearth"; ValueData: """{app}\{#MyAppExeName}"""; Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Clean up system proxy on uninstall
Filename: "reg"; Parameters: "delete ""HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings"" /v ProxyEnable /f"; Flags: runhidden

[UninstallDelete]
Type: filesandordirs; Name: "{app}\xray"
Type: filesandordirs; Name: "{localappdata}\heyearth_vpn"

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    // Ensure xray directory exists in app data too
    ForceDirectories(ExpandConstant('{localappdata}\heyearth_vpn\xray'));
  end;
end;
