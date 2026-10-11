Unicode true

SetCompressor /SOLID /FINAL lzma
SetCompressorDictSize 32

####
## Deepzora per-user NSIS installer (Electron shell).
##
## This file is COMMITTED and fully self-contained: the Electron packaging
## script (desktop/packaging/package.mjs) generates deepzora_project.nsh with
## the INFO_* identity defines, and every macro the old Wails template provided
## is inlined below. The customizations vs. a stock NSIS template:
##
##   1. REQUEST_EXECUTION_LEVEL "user" + InstallDir under $LOCALAPPDATA - install
##      without administrator rights. This lets the auto-updater re-run a freshly
##      downloaded installer in a visible progress-only mode with no UAC prompt.
##   2. Uninstall registry under HKCU (not HKLM) - a non-admin install cannot
##      write HKLM, so the uninstaller macros below use HKCU.
##   3. InstallDir is remembered across updates via InstallDirRegKey +
##      InstallLocation (HKCU\...\Uninstall\InstallLocation). When upgrading from
##      a build that did not write InstallLocation yet, .onInit falls back to the
##      old DisplayIcon path before using the default. Without this, every release
##      forces the user back to %LOCALAPPDATA%\Programs\Deepzora even if they had
##      moved the install to a different drive (e.g. D:\Tools\Deepzora); the
##      auto-updater would overwrite the wrong dir, leaving the old install
##      orphaned.
##   4. The payload is the flat Go executables plus the Electron app/ tree,
##      installed recursively with `File /r` into the versioned staging
##      directory that the signed Go activator publishes as versions/v<ver>/.
####

## Install per-user (no admin).
!define REQUEST_EXECUTION_LEVEL "user"

####
## Product identity. DEEPZORA_VERSION_TAG is the release/install identity;
## INFO_PRODUCTVERSION is numeric metadata only.
####
!if /FileExists "deepzora_project.nsh"
!include "deepzora_project.nsh"
!else
!error "deepzora_project.nsh is missing; run desktop/packaging/package.mjs first"
!endif
!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"

# The build script writes this host-specific include before invoking makensis.
# Keep a Windows fallback so opening this script directly still behaves like a
# native Windows build.
!if /FileExists "deepzora_host.nsh"
!include "deepzora_host.nsh"
!endif
!ifndef DEEPZORA_UNINST_FINALIZE
!define DEEPZORA_UNINST_FINALIZE 'cmd.exe /C copy /Y "%1" "deepzora-uninstall.exe" >NUL'
!endif

# The service executable stays the active version entry the thin launcher
# starts; it bootstraps app\Deepzora.exe (Electron) and exits.
!define PRODUCT_EXECUTABLE "${INFO_PROJECTNAME}.exe"
!define DEEPZORA_ELECTRON_EXECUTABLE "Deepzora.exe"
!define UNINST_KEY_NAME "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${UNINST_KEY_NAME}"
RequestExecutionLevel "${REQUEST_EXECUTION_LEVEL}"

# Exactly one target architecture per installer, selected by the build script
# through the binary define it passes (values point at the staged service
# executable; only their presence selects the architecture).
!ifdef ARG_DEEPZORA_AMD64_BINARY
!define ARCH "amd64"
!endif
!ifdef ARG_DEEPZORA_ARM64_BINARY
!define ARCH "arm64"
!endif
!ifndef ARCH
!error "one of ARG_DEEPZORA_AMD64_BINARY or ARG_DEEPZORA_ARM64_BINARY is required; package-windows-desktop.sh passes it"
!endif

!macro deepzora.checkArchitecture
    ${If} ${AtLeastWin10}
        !if "${ARCH}" == "amd64"
            ${if} ${IsNativeAMD64}
                Goto deepzora_arch_ok
            ${EndIf}
        !else
            ${if} ${IsNativeARM64}
                Goto deepzora_arch_ok
            ${EndIf}
        !endif

        IfSilent deepzora_arch_silent deepzora_arch_interactive
        deepzora_arch_silent:
            SetErrorLevel 65
            Abort
        deepzora_arch_interactive:
            MessageBox MB_OK "This product can't be installed on the current Windows architecture. Supports: ${ARCH}"
            Quit
    ${else}
        IfSilent deepzora_win_silent deepzora_win_interactive
        deepzora_win_silent:
            SetErrorLevel 64
            Abort
        deepzora_win_interactive:
            MessageBox MB_OK "This product is only supported on Windows 10 (Server 2016) and later."
            Quit
    ${EndIf}

    deepzora_arch_ok:
!macroend

!macro deepzora.setShellContext
    ${If} ${REQUEST_EXECUTION_LEVEL} == "admin"
        SetShellVarContext all
    ${else}
        SetShellVarContext current
    ${EndIf}
!macroend

# The release unit: the Go service executable plus the Electron app/ tree.
# package-windows-desktop.sh stages both next to this script before makensis.
!macro deepzora.files
    File "/oname=${PRODUCT_EXECUTABLE}" "${PRODUCT_EXECUTABLE}"
    !if /FileExists "app\${DEEPZORA_ELECTRON_EXECUTABLE}"
    File /r "app"
    !else
    !error "the Electron app tree is missing; run desktop/packaging/package.mjs first"
    !endif
!macroend

# Deepzora registers no file associations or custom protocols; keep the hooks
# as no-ops so the install/uninstall flow keeps its shape.
!macro deepzora.associateFiles
!macroend

!macro deepzora.unassociateFiles
!macroend

!macro deepzora.associateCustomProtocols
!macroend

!macro deepzora.unassociateCustomProtocols
!macroend

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${DEEPZORA_DISPLAY_VERSION}"
VIAddVersionKey "FileVersion"     "${DEEPZORA_DISPLAY_VERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!define MUI_PAGE_CUSTOMFUNCTION_PRE deepzora.skipSetupPageForUpdate
!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!define MUI_PAGE_CUSTOMFUNCTION_PRE deepzora.skipSetupPageForUpdate
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!define MUI_PAGE_CUSTOMFUNCTION_SHOW deepzora.showUpdateProgress
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!define MUI_PAGE_CUSTOMFUNCTION_PRE deepzora.skipFinishPageForUpdate
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "TradChinese"

LangString deepzoraUpdateTitle ${LANG_ENGLISH} "Updating Deepzora"
LangString deepzoraUpdateTitle ${LANG_SIMPCHINESE} "正在更新 Deepzora"
LangString deepzoraUpdateTitle ${LANG_TRADCHINESE} "正在更新 Deepzora"
LangString deepzoraUpdateSubtitle ${LANG_ENGLISH} "Installing the verified update. Deepzora will restart automatically."
LangString deepzoraUpdateSubtitle ${LANG_SIMPCHINESE} "正在安装已验证的更新，完成后 Deepzora 将自动重启。"
LangString deepzoraUpdateSubtitle ${LANG_TRADCHINESE} "正在安裝已驗證的更新，完成後 Deepzora 將自動重新啟動。"
LangString deepzoraActivateBusy ${LANG_ENGLISH} "Deepzora is still running or another installation is in progress. Close it and click Retry. Details: %APPDATA%\deepzora\desktop-shell\logs\recovery.log"
LangString deepzoraActivateBusy ${LANG_SIMPCHINESE} "Deepzora 仍在运行，或另一个安装正在进行。请关闭后点击“重试”。详情见 %APPDATA%\deepzora\desktop-shell\logs\recovery.log"
LangString deepzoraActivateBusy ${LANG_TRADCHINESE} "Deepzora 仍在執行，或另一個安裝正在進行。請關閉後點擊「重試」。詳情見 %APPDATA%\deepzora\desktop-shell\logs\recovery.log"
LangString deepzoraActivateLocked ${LANG_ENGLISH} "Deepzora could not activate the release, often because a file was temporarily locked by antivirus or sync software. Wait a moment and click Retry. Details: %APPDATA%\deepzora\desktop-shell\logs\recovery.log"
LangString deepzoraActivateLocked ${LANG_SIMPCHINESE} "Deepzora 无法启用新版本，通常是文件被杀毒或同步软件临时锁定。请稍候再点击“重试”。详情见 %APPDATA%\deepzora\desktop-shell\logs\recovery.log"
LangString deepzoraActivateLocked ${LANG_TRADCHINESE} "Deepzora 無法啟用新版本，通常是檔案被防毒或同步軟體暫時鎖定。請稍候再點擊「重試」。詳情見 %APPDATA%\deepzora\desktop-shell\logs\recovery.log"

## Preserve the first-pass generated uninstaller so the release workflow can
## Authenticode-sign it together with the other installed payload files.
## The second pass provides ARG_DEEPZORA_SIGNED_UNINSTALLER and embeds that
## signed binary instead of generating another unsigned uninstaller.
!ifndef ARG_DEEPZORA_SIGNED_UNINSTALLER
!uninstfinalize '${DEEPZORA_UNINST_FINALIZE}'
!endif
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!define DEEPZORA_DEFAULT_INSTALLDIR "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!define DEEPZORA_UPDATE_HELPER "deepzora-update-helper.exe"
!define DEEPZORA_LAUNCHER "deepzora-launcher.exe"
!define DEEPZORA_CLI "deepzora-cli.exe"
!define DEEPZORA_PORTABLE_ENTRY "Deepzora.exe"
!define DEEPZORA_LAYOUT_INSTALLER "deepzora-layout-installer.exe"
!define DEEPZORA_PAYLOAD_MANIFEST "deepzora-payload.json"
!define DEEPZORA_PAYLOAD_SIGNATURE "deepzora-payload.json.minisig"
Var DeepzoraUpdateMode
Var DeepzoraStageMode
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation" # Reuse the previous install path on update; .onInit falls back to the default on first install.
InstallDir "${DEEPZORA_DEFAULT_INSTALLDIR}" # Per-user install location (no admin rights required).
ShowInstDetails show # This will always show the installation details.

####
## Per-user uninstaller registry (HKCU). HKLM writes would fail without admin
## rights, so the uninstaller registration lives entirely under HKCU.
####
!macro deepzora.writeUninstaller
    !ifdef ARG_DEEPZORA_SIGNED_UNINSTALLER
    File "/oname=uninstall.exe" "${ARG_DEEPZORA_SIGNED_UNINSTALLER}"
    !else
    WriteUninstaller "$INSTDIR\uninstall.exe"
    !endif

    WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${DEEPZORA_DISPLAY_VERSION}"
    !if /FileExists "${DEEPZORA_LAUNCHER}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}"
    !else
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    !endif
    WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
    WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
    # Persist the resolved install path so a subsequent update picks it up
    # via InstallDirRegKey above. Without this, every release would force the
    # user back to %LOCALAPPDATA%\Programs\Deepzora even if they had moved
    # the install to a different drive (e.g. D:\Tools\Deepzora). The auto-
    # updater trusts this persisted path, so it has to be present before the
    # visible progress-only re-install.
    WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"

    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
!macroend


!macro deepzora.deleteUninstaller
    Delete "$INSTDIR\uninstall.exe"
    DeleteRegKey HKCU "${UNINST_KEY}"
!macroend

Function .onInit
   !ifdef ARG_DEEPZORA_UNINSTALLER_ONLY
   ; This compiler artifact exists only to extract the shared uninstaller.
   ; It is never an installable or publishable product.
   Quit
   !endif
   !insertmacro deepzora.checkArchitecture

   ; The helper passes /DEEPZORAUPDATE=1 and a final /D=<current directory>.
   ; This mode remains visible but skips every page that could change the
   ; destination, then closes automatically after the file copy so the helper
   ; can relaunch Deepzora. A normal manual installer keeps the full wizard.
   StrCpy $DeepzoraUpdateMode "0"
   StrCpy $DeepzoraStageMode "0"
   ${GetParameters} $R0
   ClearErrors
   ${GetOptions} $R0 "/DEEPZORAUPDATE=" $R1
   IfErrors deepzora_update_mode_done
   StrCmp $R1 "1" 0 deepzora_update_mode_done
   StrCpy $DeepzoraUpdateMode "1"

deepzora_update_mode_done:
   ClearErrors
   ${GetOptions} $R0 "/DEEPZORASTAGE=" $R2
   IfErrors deepzora_stage_mode_done
   StrCmp $R2 "1" 0 deepzora_stage_mode_done
   StrCpy $DeepzoraStageMode "1"

deepzora_stage_mode_done:

   StrCmp $INSTDIR "" 0 done
   StrCpy $INSTDIR "${DEEPZORA_DEFAULT_INSTALLDIR}"
done:
FunctionEnd

Function deepzora.skipSetupPageForUpdate
   StrCmp $DeepzoraUpdateMode "1" 0 deepzora_show_setup_page
   Abort

deepzora_show_setup_page:
FunctionEnd

Function deepzora.showUpdateProgress
   StrCmp $DeepzoraUpdateMode "1" 0 deepzora_update_progress_done
   !insertmacro MUI_HEADER_TEXT "$(deepzoraUpdateTitle)" "$(deepzoraUpdateSubtitle)"
   SetDetailsView hide
   SetAutoClose true
   BringToFront

deepzora_update_progress_done:
FunctionEnd

Function deepzora.skipFinishPageForUpdate
   StrCmp $DeepzoraUpdateMode "1" 0 deepzora_show_finish_page
   Abort

deepzora_show_finish_page:
FunctionEnd

# Check every stable entry point before extracting a replacement.  A running
# shell may have already exited its Go service while still holding one of
# these files open; treating that as an installable state recreates the
# "installed but does not open" failure.  Silent installs fail closed.
Function deepzora.waitForExecutableUnlock
   StrCpy $3 40
deepzora_unlock_check:
   StrCpy $2 0
   IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 deepzora_unlock_versioned
   ClearErrors
   FileOpen $1 "$INSTDIR\${PRODUCT_EXECUTABLE}" a
   IfErrors deepzora_unlock_stable_locked
   FileClose $1
   Goto deepzora_unlock_versioned
deepzora_unlock_stable_locked:
   StrCpy $2 1
deepzora_unlock_versioned:
   IfFileExists "$INSTDIR\versions\${DEEPZORA_VERSION_TAG}\${PRODUCT_EXECUTABLE}" 0 deepzora_unlock_launcher
   ClearErrors
   FileOpen $1 "$INSTDIR\versions\${DEEPZORA_VERSION_TAG}\${PRODUCT_EXECUTABLE}" a
   IfErrors deepzora_unlock_versioned_locked
   FileClose $1
   Goto deepzora_unlock_launcher
deepzora_unlock_versioned_locked:
   StrCpy $2 1
deepzora_unlock_launcher:
   IfFileExists "$INSTDIR\${DEEPZORA_LAUNCHER}" 0 deepzora_unlock_cli
   ClearErrors
   FileOpen $1 "$INSTDIR\${DEEPZORA_LAUNCHER}" a
   IfErrors deepzora_unlock_launcher_locked
   FileClose $1
   Goto deepzora_unlock_cli
deepzora_unlock_launcher_locked:
   StrCpy $2 1
deepzora_unlock_cli:
   IfFileExists "$INSTDIR\${DEEPZORA_CLI}" 0 deepzora_unlock_portable
   ClearErrors
   FileOpen $1 "$INSTDIR\${DEEPZORA_CLI}" a
   IfErrors deepzora_unlock_cli_locked
   FileClose $1
   Goto deepzora_unlock_portable
deepzora_unlock_cli_locked:
   StrCpy $2 1
deepzora_unlock_portable:
   IfFileExists "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" 0 deepzora_unlock_result
   ClearErrors
   FileOpen $1 "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" a
   IfErrors deepzora_unlock_portable_locked
   FileClose $1
   Goto deepzora_unlock_result
deepzora_unlock_portable_locked:
   StrCpy $2 1
deepzora_unlock_result:
   StrCmp $2 0 deepzora_unlock_ok
   IntOp $3 $3 - 1
   IntCmp $3 0 deepzora_unlock_failed deepzora_unlock_retry deepzora_unlock_retry
deepzora_unlock_retry:
   Sleep 500
   Goto deepzora_unlock_check
deepzora_unlock_failed:
   SetErrorLevel 1618
   IfSilent deepzora_unlock_abort deepzora_unlock_prompt
deepzora_unlock_prompt:
   MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "Deepzora is still running. Close it and click Retry, or cancel this installation." IDRETRY deepzora_unlock_check
deepzora_unlock_abort:
   Abort
deepzora_unlock_ok:
FunctionEnd


!ifdef ARG_DEEPZORA_UNINSTALLER_ONLY
Section
    WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd
!else
Section
    !insertmacro deepzora.setShellContext

    ; Staging exposes the verified current release unit. Normal installs
    ; activate it transactionally under versions/<version>.
    StrCmp $DeepzoraStageMode "1" deepzora_stage_payload
    ; The signed activator coordinates all installed versions before committing.
    Call deepzora.waitForExecutableUnlock
    Goto deepzora_normal_install

deepzora_stage_payload:
    SetOutPath $INSTDIR
    !if /FileExists "${DEEPZORA_PAYLOAD_MANIFEST}"
    File "/oname=${DEEPZORA_PAYLOAD_MANIFEST}" "${DEEPZORA_PAYLOAD_MANIFEST}"
    !endif
    !if /FileExists "${DEEPZORA_PAYLOAD_SIGNATURE}"
    File "/oname=${DEEPZORA_PAYLOAD_SIGNATURE}" "${DEEPZORA_PAYLOAD_SIGNATURE}"
    !endif
    !insertmacro deepzora.files
    !if /FileExists "${DEEPZORA_UPDATE_HELPER}"
    File "/oname=${DEEPZORA_UPDATE_HELPER}" "${DEEPZORA_UPDATE_HELPER}"
    !endif
    !if /FileExists "${DEEPZORA_LAYOUT_INSTALLER}"
    File "/oname=${DEEPZORA_LAYOUT_INSTALLER}" "${DEEPZORA_LAYOUT_INSTALLER}"
    !endif
    !if /FileExists "${DEEPZORA_LAUNCHER}"
    File "/oname=${DEEPZORA_LAUNCHER}" "${DEEPZORA_LAUNCHER}"
    !endif
    !if /FileExists "${DEEPZORA_CLI}"
    File "/oname=${DEEPZORA_CLI}" "${DEEPZORA_CLI}"
    !endif
    Goto deepzora_section_done

deepzora_normal_install:
    ; Extract into an install-local temporary directory, then let the signed Go
    ; activator validate the complete release unit, transactionally publish the
    ; version/root entries, and strictly atomically replace current.json last.
    ; The normal/recovery installer therefore shares the same commit protocol as
    ; automatic updates instead of writing live files or current.json in place.
    System::Call 'kernel32::GetCurrentProcessId() i .R8'
    CreateDirectory "$INSTDIR\versions"
    StrCpy $R9 "$INSTDIR\versions\.installer-${DEEPZORA_VERSION_TAG}-$R8"
    RMDir /r "$R9"
    CreateDirectory "$R9"
    SetOutPath "$R9"
    !insertmacro deepzora.files
    !if /FileExists "${DEEPZORA_UPDATE_HELPER}"
    File "/oname=${DEEPZORA_UPDATE_HELPER}" "${DEEPZORA_UPDATE_HELPER}"
    !else
    !warning "${DEEPZORA_UPDATE_HELPER} was not found; Windows auto-update will fail safely until the helper is installed."
    !endif
    !if /FileExists "${DEEPZORA_CLI}"
    File "/oname=${DEEPZORA_CLI}" "${DEEPZORA_CLI}"
    !else
    !warning "${DEEPZORA_CLI} was not found; remote upload installation will be unavailable."
    !endif
    !if /FileExists "${DEEPZORA_LAUNCHER}"
    File "/oname=${DEEPZORA_LAUNCHER}" "${DEEPZORA_LAUNCHER}"
    !endif

    SetOutPath "$PLUGINSDIR"
    !if /FileExists "${DEEPZORA_LAYOUT_INSTALLER}"
    File "/oname=${DEEPZORA_LAYOUT_INSTALLER}" "${DEEPZORA_LAYOUT_INSTALLER}"
    !else
    !error "${DEEPZORA_LAYOUT_INSTALLER} was not found; normal installs require the signed layout activator."
    !endif
    DetailPrint "Deepzora layout activator output:"
    StrCpy $R7 ""
    IfSilent +2 0
    StrCpy $R7 "--interactive-recovery"
deepzora_layout_activate:
    nsExec::ExecToLog /OEM '"$PLUGINSDIR\${DEEPZORA_LAYOUT_INSTALLER}" --install-root "$INSTDIR" --version "${DEEPZORA_VERSION_TAG}" --activate-staging "$R9" $R7'
    Pop $0
    StrCmp $0 "0" deepzora_layout_activated
    DetailPrint "Deepzora layout activation failed with exit code $0; the previous version remains active."
    ; 1602 is the user's own cancel in the recovery dialog. Every other failure
    ; keeps $R9 so Retry re-runs the activator against the same verified files;
    ; the exit code is set only once the attempt is truly abandoned.
    StrCmp $0 "1602" deepzora_activation_cancelled
    IfSilent deepzora_activation_failed 0
    StrCmp $0 "1618" deepzora_activation_busy_prompt deepzora_activation_locked_prompt
deepzora_activation_busy_prompt:
    MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(deepzoraActivateBusy)" IDRETRY deepzora_layout_activate
    Goto deepzora_activation_failed
deepzora_activation_locked_prompt:
    MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(deepzoraActivateLocked)" IDRETRY deepzora_layout_activate
deepzora_activation_failed:
    RMDir /r "$R9"
    StrCmp $0 "1618" 0 +3
    SetErrorLevel 1618
    Goto deepzora_activation_abort
    SetErrorLevel 1
    Goto deepzora_activation_abort
deepzora_activation_cancelled:
    RMDir /r "$R9"
    SetErrorLevel 1602
deepzora_activation_abort:
    Abort "Deepzora could not activate the verified release. The previous version was left unchanged."

deepzora_layout_activated:
    RMDir /r "$R9"
    SetOutPath "$INSTDIR"

    !if /FileExists "${DEEPZORA_LAUNCHER}"
    ; Keep both target and icon on the stable launcher. Pointing IconLocation at
    ; versions\vX\deepzora-desktop.exe leaves a blank shortcut as soon as version
    ; retention removes that directory after a later update.
    ; Preserve user arguments, icons and working directories on existing links;
    ; the owned-link repair below migrates their targets without replacing them.
    IfFileExists "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" +2 0
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" "" "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" 0
    IfFileExists "$DESKTOP\${INFO_PRODUCTNAME}.lnk" +2 0
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" "" "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" 0
    ; Stamp the exact paths created in this shell context before the user can pin them.
    nsExec::ExecToLog /OEM '"$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}" --repair-shortcuts "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$DESKTOP\${INFO_PRODUCTNAME}.lnk"'
    Pop $0
    ${If} $0 != "0"
        DetailPrint "Warning: shortcut identity repair failed ($0); the next normal launch will retry."
    ${EndIf}
    !else
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\versions\${DEEPZORA_VERSION_TAG}\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\versions\${DEEPZORA_VERSION_TAG}\${PRODUCT_EXECUTABLE}"
    !endif

    !insertmacro deepzora.associateFiles
    !insertmacro deepzora.associateCustomProtocols
    !insertmacro deepzora.writeUninstaller

deepzora_section_done:
SectionEnd
!endif

Section "uninstall"
    !insertmacro deepzora.setShellContext


    ; Precision uninstall: flat leftovers, thin entry points, and version trees.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${DEEPZORA_UPDATE_HELPER}"
    Delete "$INSTDIR\${DEEPZORA_LAUNCHER}"
    Delete "$INSTDIR\${DEEPZORA_CLI}"
    Delete "$INSTDIR\${DEEPZORA_PORTABLE_ENTRY}"
    Delete "$INSTDIR\current.json"
    RMDir /r "$INSTDIR\versions"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro deepzora.unassociateFiles
    !insertmacro deepzora.unassociateCustomProtocols

    !insertmacro deepzora.deleteUninstaller

    ; Only remove the installation directory if it is empty to prevent data loss
    RMDir $INSTDIR
SectionEnd
