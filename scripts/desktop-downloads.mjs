import { renameSync, unlinkSync, existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const desktopAssets = JSON.parse(readFileSync(new URL("../internal/releaseassets/desktop.json", import.meta.url), "utf8"));
export function desktopName(version, asset) {
  version = version.replace(/^v/, "");
  if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version)) throw new Error("invalid Desktop download version");
  return `Deepzora-${version}-${asset.suffix}`;
}

// Finalize local build outputs before signing. Only versioned names are published.
export function prepareDownloads(directory, version) {
  for (const asset of desktopAssets) {
    const source = path.join(directory, asset.buildName);
    const name = desktopName(version, asset);
    if (name !== asset.buildName && existsSync(source)) {
      if (existsSync(path.join(directory, name))) {
        if (!readFileSync(source).equals(readFileSync(path.join(directory, name)))) throw new Error(`conflicting download: ${name}`);
        unlinkSync(source);
        continue;
      }
      renameSync(source, path.join(directory, name));
    }
  }
}

export function verifyNamedDownloads(manifest) {
  if (manifest.artifact_naming !== "versioned-v1") throw new Error("unsupported artifact naming");
  const names = desktopAssets.map(spec => desktopName(manifest.version, spec));
  if (JSON.stringify(Object.keys(manifest.downloads ?? {}).sort()) !== JSON.stringify(names.sort())) throw new Error("unexpected download matrix");
  for (const spec of desktopAssets) {
    const name = desktopName(manifest.version, spec);
    const named = manifest.downloads[name];
    if (!named || !named.url.endsWith(`/${name}`)) throw new Error(`missing versioned download: ${name}`);
    if (spec.channel) {
      const routed = manifest[spec.channel]?.[spec.platform];
      if (!routed || ["url", "sig", "size", "sha256", "install_layout"].some(key => routed[key] !== named[key])) throw new Error(`updater route differs from download: ${name}`);
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
  ].join("\n");
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const [command, version, target] = process.argv.slice(2);
  if (command === "prepare") prepareDownloads(target, version);
  else if (command === "name") {
    const asset = desktopAssets.find(asset => asset.buildName === target);
    if (!asset) throw new Error(`unknown build output: ${target}`);
    console.log(desktopName(version, asset));
  }
  else if (command === "names") console.log(JSON.stringify(desktopAssets.map(asset => desktopName(version, asset))));
  else if (command === "render") console.log(renderDesktopDownloads(version));
  else if (command === "verify") verifyNamedDownloads(JSON.parse(readFileSync(version, "utf8")));
  else throw new Error("usage: desktop-downloads.mjs prepare|names|render VERSION [DIRECTORY] | verify MANIFEST");
}
