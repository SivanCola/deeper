import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { StartupSplash } from "../src/components/StartupSplash";
import { LocaleProvider, preloadLocale, useI18n } from "../src/lib/i18n";
import "../src/styles.css";
import "./startup-splash.css";

function Preview() {
  const { setPref } = useI18n();
  const [theme, setTheme] = useState("light");
  const [locale, setLocale] = useState<"zh" | "en">("zh");
  const [still, setStill] = useState(true);
  const [hold, setHold] = useState(true);
  const [visible, setVisible] = useState(true);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => { setPref(locale); }, [locale, setPref]);

  const replay = (freeze: boolean, wait: boolean) => {
    setStill(freeze);
    setHold(wait);
    setVisible(true);
    setGeneration((value) => value + 1);
  };

  return <div className={still ? "splash-preview splash-preview--still" : "splash-preview"}>
    <nav className="splash-preview__controls" aria-label="预览控制">
      <label>主题 <select aria-label="主题" value={theme} onChange={(event) => setTheme(event.target.value)}>
        <option value="light">浅色</option><option value="dark">深色</option>
      </select></label>
      <label>语言 <select aria-label="语言" value={locale} onChange={(event) => setLocale(event.target.value as "zh" | "en")}>
        <option value="zh">中文</option><option value="en">English</option>
      </select></label>
      <button onClick={() => replay(true, true)}>重播并保持画面</button>
      <button onClick={() => replay(false, false)}>真实启动</button>
      <button onClick={() => replay(false, true)}>等待工作区</button>
      <button onClick={() => setHold(false)} disabled={!hold || still}>工作区就绪</button>
      <a href="./brand-assets.html">品牌资源</a>
    </nav>
    {visible ? <StartupSplash key={generation} hold={hold} onDone={() => { if (!still) setVisible(false); }} /> :
      <main className="splash-preview__done">启动动画已结束</main>}
  </div>;
}

await preloadLocale("zh");
createRoot(document.getElementById("root")!).render(<StrictMode><LocaleProvider><Preview /></LocaleProvider></StrictMode>);
