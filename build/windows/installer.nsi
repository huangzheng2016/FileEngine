; Per-user installer: no administrator rights, installs to %LOCALAPPDATA%\Programs\FileEngine.
; Defines: FE_BINARY FE_VERSION FE_NUMERIC_VERSION FE_OUT FE_ICON FE_WEBVIEW2
Unicode true
SetCompressor /SOLID lzma
!include "MUI2.nsh"
!include "LogicLib.nsh"

Name "FileEngine"
OutFile "${FE_OUT}"
Icon "${FE_ICON}"
UninstallIcon "${FE_ICON}"
InstallDir "$LOCALAPPDATA\Programs\FileEngine"
InstallDirRegKey HKCU "Software\FileEngine" "InstallDir"
RequestExecutionLevel user
BrandingText "FileEngine ${FE_VERSION}"
VIProductVersion "${FE_NUMERIC_VERSION}"
VIAddVersionKey /LANG=2052 "ProductName" "FileEngine"
VIAddVersionKey /LANG=2052 "ProductVersion" "${FE_VERSION}"
VIAddVersionKey /LANG=2052 "FileVersion" "${FE_VERSION}"
VIAddVersionKey /LANG=2052 "FileDescription" "FileEngine 安装程序"
VIAddVersionKey /LANG=2052 "LegalCopyright" "MIT License"

!define MUI_ICON "${FE_ICON}"
!define MUI_UNICON "${FE_ICON}"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\FileEngine.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 FileEngine"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\FileEngine"

Section "FileEngine"
  SetShellVarContext current
  ; a running copy holds the exe open
  nsExec::Exec 'taskkill /IM FileEngine.exe /F'
  SetOutPath "$INSTDIR"
  File /oname=FileEngine.exe "${FE_BINARY}"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; WebView2 ships with Windows 11; older systems get it from Microsoft's bootstrapper.
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    DetailPrint "正在安装 WebView2 运行时"
    SetOutPath "$PLUGINSDIR"
    File /oname=MicrosoftEdgeWebview2Setup.exe "${FE_WEBVIEW2}"
    ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install'
    SetOutPath "$INSTDIR"
  ${EndIf}

  CreateShortcut "$SMPROGRAMS\FileEngine.lnk" "$INSTDIR\FileEngine.exe"
  CreateShortcut "$DESKTOP\FileEngine.lnk" "$INSTDIR\FileEngine.exe"
  WriteRegStr HKCU "Software\FileEngine" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "FileEngine"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${FE_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "FileEngine"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\FileEngine.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
SectionEnd

; Uninstalling keeps the data directory (tasks, settings, keys) so a reinstall picks it up.
Section "Uninstall"
  SetShellVarContext current
  nsExec::Exec 'taskkill /IM FileEngine.exe /F'
  Delete "$INSTDIR\FileEngine.exe"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\FileEngine.lnk"
  Delete "$DESKTOP\FileEngine.lnk"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  DeleteRegKey HKCU "Software\FileEngine"
SectionEnd
