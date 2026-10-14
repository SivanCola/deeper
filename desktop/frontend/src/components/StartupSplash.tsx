import { useCallback, useEffect, useRef, useState } from "react";
import logoSymbol from "../assets/logo-symbol.svg";
import orbitTrails from "../assets/startup-orbit.webp";
import { useT } from "../lib/i18n";
import { markSplashShown } from "../lib/startupSplashState";

const MIN_VISIBLE_MS = 1400;
const FADE_OUT_MS = 420;
const MAX_HOLD_MS = 6000;

export function StartupSplash({ hold, onDone }: { hold: boolean; onDone: () => void }) {
  const t = useT();
  const [minElapsed, setMinElapsed] = useState(false);
  const [forceRelease, setForceRelease] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const finishedRef = useRef(false);
  const fadeTimerRef = useRef<number | undefined>(undefined);
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;

  const finish = useCallback((skipHold = false) => {
    if (finishedRef.current) return;
    if (!skipHold && (!minElapsed || hold) && !forceRelease) return;
    finishedRef.current = true;
    setLeaving(true);
    fadeTimerRef.current = window.setTimeout(() => {
      markSplashShown();
      onDoneRef.current();
    }, FADE_OUT_MS);
  }, [minElapsed, hold, forceRelease]);

  useEffect(() => {
    const minTimer = window.setTimeout(() => setMinElapsed(true), MIN_VISIBLE_MS);
    const maxTimer = window.setTimeout(() => setForceRelease(true), MAX_HOLD_MS);
    return () => {
      window.clearTimeout(minTimer);
      window.clearTimeout(maxTimer);
      window.clearTimeout(fadeTimerRef.current);
    };
  }, []);

  useEffect(() => {
    finish();
  }, [finish]);

  useEffect(() => {
    const skip = (event: KeyboardEvent) => {
      if (event.key !== "Escape" && event.key !== "Enter" && event.key !== " ") return;
      finish(true);
    };
    window.addEventListener("keydown", skip);
    return () => window.removeEventListener("keydown", skip);
  }, [finish]);

  return (
    <div className="startup-splash" data-leaving={leaving} onClick={() => finish(true)} aria-label="Deepzora">
      <div className="startup-splash__card">
        <div className="startup-splash__brand">
          <div className="startup-splash__orbits" aria-hidden="true">
            <div className="startup-splash__orbit-plane">
              <img src={orbitTrails} alt="" draggable={false} />
            </div>
          </div>
          <div className="startup-splash__mark startup-splash__reveal" aria-hidden="true">
            <img src={logoSymbol} alt="" draggable={false} />
          </div>
          <div className="startup-splash__name startup-splash__reveal">Deepzora</div>
        </div>
        <h1 className="startup-splash__headline startup-splash__reveal">{t("app.splashHeadline")}</h1>
        <div className="startup-splash__loading startup-splash__reveal">
          <div className="startup-splash__flow" aria-hidden="true">
            <span /><span /><span />
          </div>
          <div className="startup-splash__status" role="status">{t("app.splashPreparing")}</div>
        </div>
      </div>
      <button type="button" className="startup-splash__skip startup-splash__reveal" onClick={() => finish(true)}>
        {t("app.splashSkip")}
      </button>
    </div>
  );
}
