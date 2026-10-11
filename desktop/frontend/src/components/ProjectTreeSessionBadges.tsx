import { GitBranch } from "lucide-react";

export function ProjectTreeSessionBadges({ forkedFromLabel, recoveryLabel }: {
  forkedFromLabel?: string;
  recoveryLabel?: string;
}) {
  return <>
    {forkedFromLabel && <span className="project-tree__topic-recovery" title={forkedFromLabel}><GitBranch size={10} />{forkedFromLabel}</span>}
    {recoveryLabel && <span className="project-tree__topic-recovery" title={recoveryLabel}>{recoveryLabel}</span>}
  </>;
}
