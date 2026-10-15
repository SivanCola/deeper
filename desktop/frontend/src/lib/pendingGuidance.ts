import type { StructuredInvocationSubmit } from "./invocationDisplay";

export type PendingGuidance = {
  id: string;
  text: string;
  submitText: string;
  state?: string;
  intent?: string;
  source?: string;
  blockReason?: string;
  paused?: boolean;
  recoveredCount?: number;
  structured?: StructuredInvocationSubmit;
};
