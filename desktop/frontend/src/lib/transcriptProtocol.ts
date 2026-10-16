import type { HistoryMessage, TurnStatus, WireEvent, WireCompletionSummary } from "./types";
import type { Snapshot } from "../generated/desktopContract.generated";
import type { HistorySwitchPhases } from "./sessionDiagnostics";

export interface TranscriptContentRef {
  snapshotId: string;
  recordId: string;
  path: string[];
  bytes: number;
}

export interface TranscriptRecord {
  id: string;
  order: number;
  message: HistoryMessage;
  refs: TranscriptContentRef[];
}

export interface TranscriptSnapshot extends Pick<Snapshot, "protocolVersion" | "snapshotId" | "identity" | "projectionRevision" | "coveredThroughSeq"> {
  records: TranscriptRecord[];
  activeRecords: TranscriptRecord[];
  runtime: { turnId?: string; submissionId?: string; status?: TurnStatus; phase?: string; startedAt?: number; completionSummary?: WireCompletionSummary; pendingEvents: WireEvent[] };
  activeAttempts: Array<{ id: string; messageId: string }>;
  before: number;
  hasOlder: boolean;
  totalRecords: number;
	totalTurns: number;
  stale: boolean;
  notFound?: boolean;
}

export interface TranscriptContentChunk { data: string; nextOffset: number; done: boolean; stale: boolean }

export interface TranscriptProtocolBindings {
  TranscriptFollowForTab(tabId: string, request: import("../generated/desktopContract.generated").FollowRequest): Promise<import("../generated/desktopContract.generated").TranscriptFollowResponse>;
  RemoteTranscriptFollowForTab(tabId: string, request: import("../generated/desktopContract.generated").FollowRequest): Promise<import("../generated/desktopContract.generated").TranscriptFollowResponse>;
  TranscriptContentForTab?(tabId: string, request: TranscriptContentRef & { offset: number }): Promise<TranscriptContentChunk>;
  RemoteTranscriptContentForTab?(tabId: string, request: TranscriptContentRef & { offset: number }): Promise<TranscriptContentChunk>;
  ResumeTranscriptSessionForTab?(tabID: string, path: string): Promise<HistorySwitchPhases | void>;
  OpenChannelTranscriptSessionForTab?(tabID: string, path: string): Promise<HistorySwitchPhases | void>;
}

export interface TranscriptTurnMetadata {
  samplingCount?: number;
  toolCount?: number;
  turnFinal?: boolean;
}
