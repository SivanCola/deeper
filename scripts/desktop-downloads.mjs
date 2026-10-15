import { copyFileSync, existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const desktopAssets = JSON.parse(readFileSync(new URL("../internal/releaseassets/desktop.json", import.meta.url), "utf8"));
export function desktopName(version, asset) {
  version = version.replace(/^v/, "");
  if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version)) throw new Error("invalid Desktop download version");
  return version === "0.0.1" ? asset.legacy : `Deepzora-${version}-${asset.suffix}`;
}

// Keep the exact filenames required by the already shipped v0.0.1 updater.
// Both names are sealed and signed; publication never renames an existing asset.
export function prepareDownloads(directory, version) {
  for (const asset of desktopAssets) {
    const source = path.join(directory, asset.legacy);
    const name = desktopName(version, asset);
    if (name !== asset.legacy && existsSync(source)) {
      if (existsSync(path.join(directory, name))) {
        if (!readFileSync(source).equals(readFileSync(path.join(directory, name)))) throw new Error(`conflicting download: ${name}`);
        continue;
      }
      copyFileSync(source, path.join(directory, name));
    }
  }
}

export function verifyNamedAliases(manifest) {
  if (!manifest.artifact_naming) return;
  if (manifest.artifact_naming !== "versioned-v1" || manifest.version === "v0.0.1") throw new Error("unsupported artifact naming");
  const legacy = [...Object.values(manifest.platforms ?? {}), ...Object.values(manifest.native_packages ?? {}),
    ...Object.entries(manifest.downloads ?? {}).filter(([name]) => name.startsWith("Deepzora-darwin-") || name.startsWith("Deepzora-windows-")).map(([, asset]) => asset)];
  for (const spec of desktopAssets) {
    const name = desktopName(manifest.version, spec);
    const named = manifest.downloads?.[name];
    if (!named) throw new Error(`missing versioned download: ${name}`);
    const alias = legacy.find(asset => asset.url.endsWith(`/${spec.legacy}`));
    if (alias && ["size", "sha256", "install_layout"].some(key => alias[key] !== named[key])) {
      throw new Error(`versioned download differs from compatibility alias: ${name}`);
    }
  }
}

export function renderDesktopDownloads(version, lang = "zh", repository = "SivanCola/deepzora") {
  const zh = lang === "zh";
  const base = `https://github.com/${repository}/releases/download/desktop-v${version.replace(/^v/, "")}/`;
  const rows = desktopAssets.filter(asset => !asset.updaterOnly).map(asset => {
    const name = desktopName(version, asset);
    return `| ${zh ? asset.labelZh : asset.labelEn} | ${zh ? asset.purposeZh : asset.purposeEn} | [${zh ? "下载" : "Download"}](${base}${name}) |`;
  });
  return [
    `## ${zh ? "选择桌面版下载" : "Choose your Desktop download"}`, "",
    zh ? "Mac M 系列芯片请选择 Apple Silicon；常见 Intel／AMD Windows 电脑请选择 x64，ARM 设备请选择 ARM64。" : "For M-series Macs choose Apple Silicon. Most Intel / AMD Windows PCs use x64; ARM devices use ARM64.", "",
    zh ? "| 系统／设备 | 用途 | 下载 |" : "| System / device | Package | Download |",
    "| --- | --- | --- |", ...rows, "",
    zh ? "Mac 常规安装请选择 DMG；macOS ZIP 用于自动更新。`.minisig` 是完整性验证文件，`latest.json` 是自动更新元数据，普通安装无需单独下载。`Source code` 是源码，不能直接安装。" : "Choose a DMG for normal Mac installation; macOS ZIP files serve the updater. `.minisig` files verify integrity and `latest.json` contains updater metadata; neither is a separate installer. Source code archives are not installers.", "",
    ...(version.replace(/^v/, "") === "0.0.1" ? [] : [zh ? "Assets 中的旧格式名称用于兼容 v0.0.1 自动更新；手动下载请选择上表链接。" : "Legacy asset names support v0.0.1 automatic updates. For manual downloads use the links above.", ""]),
  ].join("\n");
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const [command, version, target] = process.argv.slice(2);
  if (command === "prepare") prepareDownloads(target, version);
  else if (command === "names") console.log(JSON.stringify(desktopAssets.map(asset => desktopName(version, asset))));
  else if (command === "render") console.log(renderDesktopDownloads(version));
  else if (command === "verify") verifyNamedAliases(JSON.parse(readFileSync(version, "utf8")));
  else throw new Error("usage: desktop-downloads.mjs prepare|names|render VERSION [DIRECTORY] | verify MANIFEST");
}
