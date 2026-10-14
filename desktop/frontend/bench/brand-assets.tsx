import { createRoot } from "react-dom/client";
import symbol from "../src/assets/logo-symbol.svg";
import wordmark from "../src/assets/logo-wordmark.svg";
import app from "../../build/appicon.png";
import mac from "../../build/darwin/appicon.png";
import tray from "../../build/trayTemplate@2x.png";
import icon16 from "../../build/linux/icons/hicolor/16x16/apps/deepzora-desktop.png";
import icon24 from "../../build/linux/icons/hicolor/24x24/apps/deepzora-desktop.png";
import icon32 from "../../build/linux/icons/hicolor/32x32/apps/deepzora-desktop.png";
import icon48 from "../../build/linux/icons/hicolor/48x48/apps/deepzora-desktop.png";
import icon64 from "../../build/linux/icons/hicolor/64x64/apps/deepzora-desktop.png";
import "./brand-assets.css";

function Preview() {
  return <main>
    <header><h1>Deepzora · 橙白折带</h1><a href="./startup-splash.html">查看启动动画 →</a></header>
    <div className="brand-assets__themes">
      {["light", "dark"].map((theme) => <section className={`brand-assets__surface brand-assets__surface--${theme}`} key={theme}>
        <h2>{theme === "light" ? "浅色主题" : "深色主题"}</h2>
        <img src={symbol} className="brand-assets__symbol" alt="Deepzora 立体折带 Logo" />
        <img src={wordmark} className="brand-assets__wordmark" alt="Deepzora 字标" />
        <div className="brand-assets__sizes">{[[16,icon16],[24,icon24],[32,icon32],[48,icon48],[64,icon64]].map(([size,src]) =>
          <figure key={size}><img src={String(src)} width={Number(size)} height={Number(size)} alt={`${size}px 应用图标`} /><figcaption>{size}px</figcaption></figure>)}</div>
      </section>)}
    </div>
    <section className="brand-assets__native">
      <figure><img src={mac} width="100" height="100" alt="macOS 应用图标" /><figcaption>macOS</figcaption></figure>
      <figure><img src={app} width="100" height="100" alt="Windows 与 Linux 应用图标" /><figcaption>Windows · Linux</figcaption></figure>
      <figure><img src={tray} width="36" height="36" alt="菜单栏单色图标" /><figcaption>菜单栏 · Retina</figcaption></figure>
    </section>
  </main>;
}
createRoot(document.getElementById("root")!).render(<Preview />);
