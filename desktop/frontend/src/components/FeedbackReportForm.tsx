import { useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useProviderT } from "../lib/providerSettingsLocale";
import { ErrorMessage } from "./ErrorMessage";

export function FeedbackReportForm() {
  const t = useProviderT();
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const inFlight = useRef(false);

  async function submit() {
    const text = message.trim();
    if (!text || inFlight.current) return;
    inFlight.current = true;
    setSending(true);
    setError(null);
    setSent(false);
    try {
      await app.ReportCrash("feedback", JSON.stringify({
        schemaVersion: 2, kind: "feedback", source: "frontend",
        label: "user-feedback", message: text, occurredAt: new Date().toISOString(),
      }));
      setSent(true);
    } catch (error) {
      setError(error);
    } finally {
      inFlight.current = false;
      setSending(false);
    }
  }

  return <div className="feedback-report-form">
    <label>
      <span>{t("feedback.message")}</span>
      <textarea className="mem-textarea" rows={4} maxLength={4000}
        value={message} disabled={sending} placeholder={t("feedback.placeholder")}
        onChange={(event) => { setMessage(event.target.value); setSent(false); setError(null); }} />
    </label>
    <div className="settings-inline-controls">
      <button type="button" className="btn btn--small" disabled={sending || !message.trim() || sent}
        onClick={() => void submit()}>{t(sending ? "crash.sending" : "feedback.send")}</button>
      {sent && <span role="status">{t("feedback.sent")}</span>}
    </div>
    {error != null && <div role="alert"><ErrorMessage error={error} /></div>}
  </div>;
}
