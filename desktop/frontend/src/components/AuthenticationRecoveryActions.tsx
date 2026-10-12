import { app } from "../lib/bridge";
import { useI18n } from "../lib/i18n";
import { useToast } from "../lib/toast";
import type { TabMeta } from "../lib/types";
import { useAppNavigationStore } from "../store/appNavigation";
import "./AuthenticationRecoveryActions.css";

const COPY = {
  en: {
    missing: "This model connection needs an API key.",
    rejected: "The provider rejected this connection's credential.",
    unavailable: "Deepzora could not read the credential store.",
    configure: "Configure credential",
    retry: "Retry once",
    authorized: "One authentication retry is ready. Send your message when ready.",
  },
  zh: {
    missing: "当前模型连接需要 API Key。",
    rejected: "Provider 拒绝了当前连接的凭据。",
    unavailable: "Deepzora 无法读取凭据存储。",
    configure: "配置凭据",
    retry: "重试一次",
    authorized: "已允许一次认证重试，可以重新发送消息。",
  },
} as const;

export function AuthenticationRecoveryActions({
  authentication,
  tabId,
}: {
  authentication: NonNullable<TabMeta["authentication"]>;
  tabId?: string;
}) {
  const { locale } = useI18n();
  const copy = COPY[locale];
  const { showToast } = useToast();
  return <>
    <span className="composer-toolbar-send__hint">{
      authentication.status === "authentication_rejected" ? copy.rejected
        : authentication.status === "credential_store_unavailable" ? copy.unavailable
          : copy.missing
    }</span>
    {authentication.providerName && <button type="button" className="composer-toolbar-send__configure" onClick={() => {
      useAppNavigationStore.getState().setSettingsFocus((current) => ({
        target: "model-access",
        providerName: authentication.providerName,
        sourceTabId: tabId,
        requestId: (current?.requestId ?? 0) + 1,
      }));
      useAppNavigationStore.getState().setSettingsTarget("models");
    }}>{copy.configure}</button>}
    {authentication.status === "authentication_rejected" && <button type="button" className="composer-toolbar-send__configure" onClick={() => void (async () => {
      try {
        await app.RetryAuthenticationForTab(tabId ?? "");
        showToast(copy.authorized, "info");
      } catch (error) {
        showToast(String(error), "warn");
      }
    })()}>{copy.retry}</button>}
  </>;
}
