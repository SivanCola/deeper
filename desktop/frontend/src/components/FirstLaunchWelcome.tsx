import { Folder, Link } from "lucide-react";
import { useT } from "../lib/i18n";

export type FirstLaunchWelcomeProps = {
  onConfigureProvider: () => void;
  onChooseProject: () => void;
  addingProject?: boolean;
  configured?: boolean;
};

export function FirstLaunchWelcome({ onConfigureProvider, onChooseProject, addingProject = false, configured = false }: FirstLaunchWelcomeProps) {
  const t = useT();
  return <section className="first-launch-welcome" aria-labelledby="first-launch-title">
    <h2 id="first-launch-title" className="first-launch-welcome__title">{t("welcome.setup.title")}</h2>
    <p className="first-launch-welcome__description">{t(configured ? "welcome.setup.readyDescription" : "welcome.setup.description")}</p>
    <div className="first-launch-welcome__composer">
      <p className="first-launch-welcome__placeholder">{t(configured ? "welcome.setup.readyPlaceholder" : "welcome.setup.placeholder")}</p>
      <div className="first-launch-welcome__actions">
        <span className="first-launch-welcome__status"><Link size={16} aria-hidden="true" />{t(configured ? "welcome.setup.connected" : "welcome.setup.disconnected")}</span>
        <button type="button" className="btn btn--primary first-launch-welcome__connect" onClick={onConfigureProvider}>
          {t(configured ? "welcome.setup.startConversation" : "welcome.setup.connect")}
        </button>
      </div>
    </div>
    <div className="first-launch-welcome__project">
      <button type="button" className="first-launch-welcome__project-button" disabled={addingProject} aria-busy={addingProject} onClick={onChooseProject}>
        <Folder size={16} aria-hidden="true" />{t(addingProject ? "welcome.setup.addingProject" : "welcome.setup.addProject")}
      </button>
      <span className="first-launch-welcome__project-note">{t("welcome.setup.projectHint")}</span>
    </div>
  </section>;
}
