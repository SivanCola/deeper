import { GitBranch } from "lucide-react";

export function ProjectTreeSessionBadges({ forkedFromLabel }: {
  forkedFromLabel?: string;
}) {
  return forkedFromLabel ? <span className="project-tree__topic-origin" title={forkedFromLabel}><GitBranch size={10} />{forkedFromLabel}</span> : null;
}
