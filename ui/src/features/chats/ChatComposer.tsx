import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type RefObject,
  type SyntheticEvent,
} from "react";

import { useChat, type ChatTurnKind } from "../../app/state/chat";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useRuntime } from "../../app/state/runtime";
import { useSettings } from "../../app/state/settings";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useChatTarget } from "../../app/state/derived";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import type { QueuedChatMessage } from "../../app/state/_shared";
import { type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import { describeGatewayError, formatErrorCode } from "../../lib/error-diagnostics";
import { usePersistedState } from "../../lib/persistedState";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import { providerDisplayName } from "../../lib/provider-utils";
import type { ChatAvailableCommandRecord, ChatConfigOptionRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type {
  ConfiguredProviderRecord,
  ProviderPresetRecord,
  ProviderRecord,
} from "../../types/provider";
import { Icon, Icons, InlineError } from "../shared/ui";
import { MCPServerEditor } from "../shared/MCPServerEditor";

import {
  ExternalAgentConfigControls,
  HecateModelConfigControl,
  HecateProviderConfigControl,
  LockedHecateModelSnapshot,
} from "./ChatAgentControls";
import { ChatDictationControl } from "./ChatDictationControl";
import { ChatNoticeInline } from "./ChatNotice";
import {
  appendChatFiles,
  ChatAttachmentDrafts,
  type ChatAttachmentAcceptance,
} from "./ChatImageAttachments";
import { mergeAgentConfigOptions } from "./agentConfigOptions";
import { mcpServerFormEntriesToPayload } from "../../lib/mcp-server-form";

const COMPOSER_TEXTAREA_MAX_LINES = 10;
const COMPOSER_TEXTAREA_MIN_HEIGHT = 42;
const PROJECT_PROPOSAL_COMMAND = {
  name: "proposal",
  description: "Draft a Project Assistant proposal",
  inputHint: "request",
};
const HECATE_MESSAGE_COMMANDS: Record<
  HecateMessageCommandName,
  { description: string; inputHint?: string }
> = {
  proposal: PROJECT_PROPOSAL_COMMAND,
  plan: {
    description: "Draft a Project Assistant plan",
    inputHint: "request",
  },
  work: {
    description: "Draft project work from this chat",
    inputHint: "title or request",
  },
  handoff: {
    description: "Draft a project handoff proposal",
    inputHint: "role and request",
  },
  review: {
    description: "Draft a project review proposal",
    inputHint: "target",
  },
  diff: {
    description: "Open workspace changes",
  },
  model: {
    description: "Open chat model controls",
  },
  settings: {
    description: "Open chat settings",
  },
  status: {
    description: "Open chat status details",
  },
  context: {
    description: "Open chat context details",
  },
  compact: {
    description: "Compact older chat context",
  },
  task: {
    description: "Open the active task or Tasks",
  },
  project: {
    description: "Open the linked project",
  },
  connections: {
    description: "Open Connections",
  },
};
// Keep proposal-oriented project commands here so the picker labels them as
// Project, not as generic Hecate navigation/runtime shortcuts.
const HECATE_PROJECT_COMMAND_NAMES = new Set<string>([
  "proposal",
  "plan",
  "work",
  "handoff",
  "review",
]);

type HecateMessageCommandName =
  | "proposal"
  | "plan"
  | "work"
  | "handoff"
  | "review"
  | "diff"
  | "model"
  | "settings"
  | "status"
  | "context"
  | "compact"
  | "task"
  | "project"
  | "connections";
type ComposerTextareaNumericStyle =
  | "paddingTop"
  | "paddingBottom"
  | "borderTopWidth"
  | "borderBottomWidth";

type HecateProviderOption = {
  id: string;
  name: string;
  healthy: boolean;
  kind?: string;
  configured?: boolean;
  disabledReason?: string;
};

type MessageCommandSuggestion = {
  kind: "external_agent" | "hecate";
  name: string;
  description?: string;
  inputHint?: string;
  sourceLabel?: string;
};

export type ChatComposerProps = {
  // Active chat shape (derived in ChatView and threaded through here).
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  hecateTaskToolsAvailable: boolean;
  activeRunKind: ChatTurnKind;
  activeSessionID: string;

  // Cross-region ref. ChatView owns creation so onSelectSession can
  // focus the textarea without reaching into composer internals.
  textareaRef: RefObject<HTMLTextAreaElement | null>;

  // Composer gating.
  composerVisible: boolean;
  composerInputDisabled: boolean;
  composerRouteControlsDisabled: boolean;
  composerRepair: ChatSetupRepairState | null;
  suppressChatError?: boolean;
  messageControlsVisible: boolean;
  messageSendBlocked: boolean;
  sendDisabled: boolean;
  agentBusy: boolean;
  activeRunCancellationAvailable: boolean;
  attachmentTurnInFlight: boolean;
  queueingMessage: boolean;
  attachmentAcceptance: ChatAttachmentAcceptance;
  attachmentsEnabled: boolean;
  attachmentsDisabledReason: string;
  selectedModelIssue: SelectedModelIssue | null;
  chatDiagnostic: ReturnType<typeof describeGatewayError>;

  // Hecate-chat config view.
  hecateAgentModelLocked: boolean;
  hecateChatProviderValue: string;
  hecateChatModelValue: string;
  hecateProviderOptions: HecateProviderOption[];
  hecateDisabledProviderReasons: Map<string, string>;
  selectableModels: ModelRecord[];
  onHecateModelChange: (model: string) => void;

  // Repair actions.
  chooseWorkspace: () => Promise<void> | void;
  openExternalAgentSetup: () => void;

  // Active task tracking + queue.
  activeHecateTaskID: string;
  activeHecateRunID: string;
  // Filtered to the active session — ChatView already does the filter.
  activeQueuedChatMessages: QueuedChatMessage[];
  projectProposalAvailable?: boolean;
  projectProposalDrafting?: boolean;
  workspaceChangesAvailable?: boolean;

  // User-message history feeds the arrow-key recall, derived in
  // ChatView from visibleMessages.
  messageHistory: string[];

  // Threaded from ChatView's own Props.
  onNavigate?: (workspace: "connections" | "runs" | "overview" | "settings" | "projects") => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenTrace?: (requestID: string) => void;
  onDraftProjectProposal?: (request?: string) => void;
  onOpenWorkspaceChanges?: () => void;
  onOpenChatSettings?: () => void;
  onOpenLinkedProject?: () => void;
};

export function ChatComposer(props: ChatComposerProps) {
  const runtime = useRuntime();
  const chat = useChat();
  const providersAndModels = useProvidersAndModels();
  const settings = useSettings();
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  // Pull the slice fields the composer reads. Destructured to keep the
  // rest of the component readable.
  const message = runtime.state.message;
  const rawChatError = chat.state.chatError;
  const chatErrorAction = chat.state.chatErrorAction;
  const chatErrorCode = chat.state.chatErrorCode;
  const chatErrorRequestID = chat.state.chatErrorRequestID;
  const chatErrorStatus = chat.state.chatErrorStatus;
  const chatErrorTraceID = chat.state.chatErrorTraceID;
  const rawChatCancelling = chat.state.chatCancelling;
  const providerFilter = chat.state.providerFilter;
  const model = chat.state.model;
  const activeChatSession = chat.state.activeChatSession;
  const runtimeHeaders = runtime.state.runtimeHeaders;
  const providerPresets = providersAndModels.state.providerPresets;
  const providers = providersAndModels.state.providers;
  const selectedDraftAgent = providersAndModels.state.agentAdapters.find(
    (adapter) => adapter.id === chat.state.agentAdapterID,
  );
  const draftAgentConfigOptions = mergeAgentConfigOptions(
    selectedDraftAgent?.config_options ?? [],
    chat.state.agentConfigOptions,
  );
  const {
    isAgentChat,
    isHecateChat,
    isExternalAgentChat,
    hecateTaskToolsAvailable,
    activeRunKind,
    activeSessionID,
    textareaRef,
    composerVisible,
    composerInputDisabled,
    composerRouteControlsDisabled,
    composerRepair,
    suppressChatError = false,
    messageControlsVisible,
    messageSendBlocked,
    sendDisabled,
    agentBusy,
    activeRunCancellationAvailable,
    attachmentTurnInFlight,
    queueingMessage,
    attachmentAcceptance,
    attachmentsEnabled,
    attachmentsDisabledReason,
    selectedModelIssue,
    chatDiagnostic,
    hecateAgentModelLocked,
    hecateChatProviderValue,
    hecateChatModelValue,
    hecateProviderOptions,
    hecateDisabledProviderReasons,
    selectableModels,
    onHecateModelChange,
    chooseWorkspace,
    openExternalAgentSetup,
    activeHecateTaskID,
    activeHecateRunID,
    activeQueuedChatMessages,
    projectProposalAvailable = false,
    projectProposalDrafting = false,
    workspaceChangesAvailable = false,
    messageHistory,
    onNavigate,
    onOpenTask,
    onOpenTrace,
    onDraftProjectProposal,
    onOpenWorkspaceChanges,
    onOpenChatSettings,
    onOpenLinkedProject,
  } = props;
  const chatCancelling =
    rawChatCancelling && chat.state.chatCancellingSessionID === activeSessionID;
  const anotherChatCancelling = rawChatCancelling && !chatCancelling;
  const stopActionLabel =
    activeRunKind === "external_agent"
      ? "Stop external agent"
      : activeRunKind === "direct_model"
        ? "Stop model response"
        : "Stop active task";
  const stoppingStatusText =
    activeRunKind === "external_agent"
      ? "Stopping external agent..."
      : activeRunKind === "direct_model"
        ? "Stopping model response..."
        : "Stopping active task...";
  const activeExternalAgentID =
    activeChatSession?.agent_id && activeChatSession.agent_id !== "hecate"
      ? activeChatSession.agent_id
      : "";
  const selectedActiveAgent = activeExternalAgentID
    ? providersAndModels.state.agentAdapters.find((adapter) => adapter.id === activeExternalAgentID)
    : undefined;
  const activeAgentConfigOptions = activeExternalAgentID
    ? mergeAgentConfigOptions(
        selectedActiveAgent?.config_options ?? [],
        activeChatSession?.config_options ?? [],
      )
    : [];
  const externalConfigSession = activeExternalAgentID
    ? { ...activeChatSession!, config_options: activeAgentConfigOptions }
    : isExternalAgentChat && draftAgentConfigOptions.length > 0
      ? {
          id: "__draft__",
          agent_id: chat.state.agentAdapterID,
          config_options: draftAgentConfigOptions,
        }
      : null;
  const draftMCPServerCount = mcpServerFormEntriesToPayload(chat.state.agentMCPServers, {
    includeApprovalPolicy: false,
  }).length;
  const chatError = suppressChatError ? "" : rawChatError;
  const composerNoticeVisible = Boolean(
    composerRepair || chatError || (isHecateChat && selectedModelIssue),
  );
  const activeRunStatusText = attachmentTurnInFlight
    ? "Chat is processing attached files. New text stays in the composer until the response finishes."
    : agentBusy
      ? isExternalAgentChat
        ? "External Agent is working. New messages will queue."
        : "Hecate Chat is working. New messages will queue."
      : "";
  const baselineComposerStatus = isExternalAgentChat
    ? "External agents run as your OS user in the selected workspace — no sandbox"
    : hecateTaskToolsAvailable
      ? "Tools use task approvals and per-call sandboxing in the selected workspace."
      : "";
  const composerStatusText = activeRunStatusText || baselineComposerStatus;

  const isMac = typeof navigator !== "undefined" && /mac/i.test(navigator.platform);
  const modKey = isMac ? "⌘" : "Ctrl";
  const [modEnterMode, setModEnterMode] = usePersistedState<boolean>(
    "hecate.shiftEnterMode",
    (raw) => (raw === "1" ? true : raw === "0" ? false : null),
    false,
    { serialize: (v) => (v ? "1" : "0") },
  );
  const formRef = useRef<HTMLFormElement>(null);
  const messageHistoryCursorRef = useRef<number | null>(null);
  const messageHistoryPendingTextRef = useRef("");
  const commandOptionRefs = useRef<Array<HTMLDivElement | null>>([]);
  const commandListboxID = useId();
  const [attachmentSelectionError, setAttachmentSelectionError] = useState("");
  const previousPendingAttachmentCountRef = useRef(chat.state.pendingChatAttachments.length);
  const [commandPickerDismissed, setCommandPickerDismissed] = useState(false);
  const [activeCommandIndex, setActiveCommandIndex] = useState(0);
  const [composerFocused, setComposerFocused] = useState(false);
  const commandQuery = messageCommandQuery(message);
  const commandSuggestions = useMemo(() => {
    if (commandQuery === null) return [];
    const query = commandQuery.toLowerCase();
    const suggestions: MessageCommandSuggestion[] = [];

    if (isHecateChat) {
      for (const command of availableHecateMessageCommands({
        projectProposalAvailable:
          projectProposalAvailable && !projectProposalDrafting && Boolean(onDraftProjectProposal),
        workspaceChangesAvailable: workspaceChangesAvailable && Boolean(onOpenWorkspaceChanges),
        chatSettingsAvailable: Boolean(onOpenChatSettings),
        compactAvailable: Boolean(activeSessionID),
        taskAvailable: Boolean(onOpenTask && activeHecateTaskID) || Boolean(onNavigate),
        projectAvailable: Boolean(onOpenLinkedProject),
        connectionsAvailable: Boolean(onNavigate),
      })) {
        if (!command.name.startsWith(query)) continue;
        suggestions.push({
          kind: "hecate",
          name: command.name,
          description: command.description,
          inputHint: command.inputHint,
          sourceLabel: hecateMessageCommandSourceLabel(command.name),
        });
      }
    }

    if (isExternalAgentChat) {
      const commands = activeChatSession?.available_commands ?? [];
      suggestions.push(
        ...commands
          .map((command) => ({
            kind: "external_agent" as const,
            name: externalAgentCommandName(command),
            description: command.description,
            inputHint: command.input_hint,
            sourceLabel: "External Agent",
          }))
          .filter((command) => command.name !== "")
          .filter((command) => command.name.toLowerCase().startsWith(query)),
      );
    }

    return suggestions;
  }, [
    activeChatSession?.available_commands,
    activeHecateTaskID,
    activeSessionID,
    commandQuery,
    isExternalAgentChat,
    isHecateChat,
    onDraftProjectProposal,
    onNavigate,
    onOpenChatSettings,
    onOpenLinkedProject,
    onOpenTask,
    onOpenWorkspaceChanges,
    projectProposalAvailable,
    projectProposalDrafting,
    workspaceChangesAvailable,
  ]);
  const commandPickerVisible =
    composerVisible && !commandPickerDismissed && commandSuggestions.length > 0;
  const activeCommandOptionID = commandPickerVisible
    ? `${commandListboxID}-option-${activeCommandIndex}`
    : undefined;

  useEffect(() => {
    setCommandPickerDismissed(false);
    setActiveCommandIndex(0);
  }, [activeSessionID, commandQuery]);

  useEffect(() => {
    setActiveCommandIndex((current) =>
      commandSuggestions.length === 0 ? 0 : Math.min(current, commandSuggestions.length - 1),
    );
  }, [commandSuggestions.length]);

  useEffect(() => {
    commandOptionRefs.current = commandOptionRefs.current.slice(0, commandSuggestions.length);
  }, [commandSuggestions.length]);

  useEffect(() => {
    if (!commandPickerVisible) return;
    commandOptionRefs.current[activeCommandIndex]?.scrollIntoView({ block: "nearest" });
  }, [activeCommandIndex, commandPickerVisible]);

  // Reset history navigation on session change. Scroll-side reset
  // lives in ChatView since it concerns the transcript surface.
  useEffect(() => {
    messageHistoryCursorRef.current = null;
    messageHistoryPendingTextRef.current = "";
    setAttachmentSelectionError("");
  }, [activeSessionID]);

  useEffect(() => {
    const previousCount = previousPendingAttachmentCountRef.current;
    const currentCount = chat.state.pendingChatAttachments.length;
    previousPendingAttachmentCountRef.current = currentCount;
    if (previousCount > 0 && currentCount === 0) {
      setAttachmentSelectionError("");
    }
  }, [chat.state.pendingChatAttachments.length]);

  useEffect(() => {
    const node = textareaRef.current;
    if (!node || !composerVisible) return;
    adjustComposerTextareaHeight(node);
  }, [composerVisible, message, textareaRef]);

  function setComposerText(value: string, cursorAtEnd = false) {
    runtime.actions.setMessage(value);
    if (!cursorAtEnd) return;
    requestAnimationFrame(() => {
      const node = textareaRef.current;
      if (!node) return;
      const end = node.value.length;
      node.setSelectionRange(end, end);
    });
  }

  function handleMessageChange(value: string) {
    messageHistoryCursorRef.current = null;
    messageHistoryPendingTextRef.current = value;
    runtime.actions.setMessage(value);
  }

  function insertDictationTranscript(transcript: string) {
    const node = textareaRef.current;
    const selectionStart = node?.selectionStart ?? message.length;
    const selectionEnd = node?.selectionEnd ?? selectionStart;
    const insertion = insertTranscriptAtSelection(
      message,
      transcript,
      selectionStart,
      selectionEnd,
    );
    messageHistoryCursorRef.current = null;
    messageHistoryPendingTextRef.current = insertion.value;
    runtime.actions.setMessage(insertion.value);
    requestAnimationFrame(() => {
      const textarea = textareaRef.current;
      if (!textarea) return;
      textarea.focus();
      textarea.setSelectionRange(insertion.cursor, insertion.cursor);
    });
  }

  function addPendingFiles(files: File[]) {
    if (chat.actions.isChatOwnershipMutationInFlight()) {
      setAttachmentSelectionError(
        "Wait for the current chat ownership change to finish before attaching files.",
      );
      return;
    }
    if (!attachmentsEnabled || files.length === 0) return;
    const result = appendChatFiles(chat.state.pendingChatAttachments, files, attachmentAcceptance);
    chat.actions.setPendingChatAttachments(result.attachments);
    setAttachmentSelectionError(result.error);
  }

  function removePendingFile(id: string) {
    chat.actions.setPendingChatAttachments((current) =>
      current.filter((attachment) => attachment.id !== id),
    );
    setAttachmentSelectionError("");
  }

  function handleMessageHistoryKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return false;
    if (messageHistory.length === 0) return false;

    const node = e.currentTarget;
    const selectionStart = node.selectionStart ?? 0;
    const selectionEnd = node.selectionEnd ?? 0;
    const hasSelection = selectionStart !== selectionEnd;
    const browsing = messageHistoryCursorRef.current !== null;
    const isEmpty = message.length === 0;
    const singleLine = !message.includes("\n");
    const atStart = selectionStart === 0 && selectionEnd === 0;
    const atEnd = selectionStart === message.length && selectionEnd === message.length;

    if (hasSelection) return false;

    if (e.key === "ArrowUp") {
      // Preserve normal multiline navigation unless the operator is
      // deliberately at the top of the composer or already browsing.
      if (!singleLine && !isEmpty && !atStart && !browsing) return false;
      e.preventDefault();
      if (!browsing) {
        messageHistoryPendingTextRef.current = message;
      }
      const current = messageHistoryCursorRef.current;
      const next = current === null ? messageHistory.length - 1 : Math.max(0, current - 1);
      messageHistoryCursorRef.current = next;
      setComposerText(messageHistory[next], true);
      return true;
    }

    if (!singleLine && !isEmpty && !atEnd && !browsing) return false;
    e.preventDefault();
    const current = messageHistoryCursorRef.current;
    if (current === null) return true;
    const next = current + 1;
    if (next >= messageHistory.length) {
      messageHistoryCursorRef.current = null;
      setComposerText(messageHistoryPendingTextRef.current, true);
      return true;
    }
    messageHistoryCursorRef.current = next;
    setComposerText(messageHistory[next], true);
    return true;
  }

  function selectCommandSuggestion(command: MessageCommandSuggestion) {
    const nextMessage = messageCommandInsertion(command);
    if (!nextMessage) return;
    messageHistoryCursorRef.current = null;
    messageHistoryPendingTextRef.current = nextMessage;
    setCommandPickerDismissed(true);
    setComposerText(nextMessage, true);
  }

  function handleCommandPickerKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (!commandPickerVisible) return false;
    if (e.key === "Escape") {
      e.preventDefault();
      setCommandPickerDismissed(true);
      return true;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveCommandIndex((current) => (current + 1) % commandSuggestions.length);
      return true;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveCommandIndex(
        (current) => (current - 1 + commandSuggestions.length) % commandSuggestions.length,
      );
      return true;
    }
    if (e.key === "Enter" || e.key === "Tab") {
      const command = commandSuggestions[activeCommandIndex];
      if (!command) return false;
      e.preventDefault();
      selectCommandSuggestion(command);
      return true;
    }
    return false;
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (composerInputDisabled) {
      if (e.key === "Enter") e.preventDefault();
      return;
    }
    if (handleCommandPickerKey(e)) return;
    if (handleMessageHistoryKey(e)) return;
    if (e.key !== "Enter") return;
    const modPressed = isMac ? e.metaKey : e.ctrlKey;

    if (attachmentTurnInFlight) {
      const sendGesture = modEnterMode ? modPressed : !e.shiftKey && !modPressed;
      if (sendGesture) e.preventDefault();
      return;
    }

    if (modEnterMode) {
      // ⌘/Ctrl+Enter sends; plain Enter is a newline (default behaviour)
      if (modPressed) {
        e.preventDefault();
        formRef.current?.requestSubmit();
      }
    } else {
      // Enter sends; Shift+Enter or ⌘/Ctrl+Enter inserts a newline
      if (e.shiftKey || modPressed) return;
      e.preventDefault();
      formRef.current?.requestSubmit();
    }
  }

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    if (attachmentTurnInFlight) {
      e.preventDefault();
      return;
    }
    const hecateCommand = parseHecateMessageCommand(message);
    if (
      chat.state.pendingChatAttachments.length === 0 &&
      hecateCommand &&
      isHecateChat &&
      hecateCommandDefinition(hecateCommand.name)
    ) {
      e.preventDefault();
      void handleHecateMessageCommand(hecateCommand);
      if (textareaRef.current) {
        textareaRef.current.style.height = "auto";
      }
      return;
    }
    void chatActions.submitChat(e);
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  }

  function clearLocalCommandMessage() {
    messageHistoryCursorRef.current = null;
    messageHistoryPendingTextRef.current = "";
    runtime.actions.setMessage("");
  }

  async function handleHecateMessageCommand(command: ParsedHecateMessageCommand) {
    const name = command.name;
    if (HECATE_PROJECT_COMMAND_NAMES.has(name)) {
      if (!projectProposalAvailable || projectProposalDrafting || !onDraftProjectProposal) {
        settingsActions.setNoticeMessage(
          "error",
          "This command needs an idle Hecate chat linked to a project.",
        );
        return;
      }
      if (!command.args) {
        settingsActions.setNoticeMessage("error", `Add a request after /${name}.`);
        return;
      }
      onDraftProjectProposal(projectAssistantRequestForHecateCommand(name, command.args));
      return;
    }

    switch (name) {
      case "diff":
        if (!workspaceChangesAvailable || !onOpenWorkspaceChanges) {
          settingsActions.setNoticeMessage("error", "Choose a workspace before using /diff.");
          return;
        }
        onOpenWorkspaceChanges();
        clearLocalCommandMessage();
        return;
      case "model":
      case "settings":
      case "status":
      case "context":
        if (!onOpenChatSettings) {
          settingsActions.setNoticeMessage("error", "Chat settings are not available here.");
          return;
        }
        onOpenChatSettings();
        clearLocalCommandMessage();
        return;
      case "compact":
        if (!activeSessionID) {
          settingsActions.setNoticeMessage("error", "Open a Hecate chat before using /compact.");
          return;
        }
        if (await chatActions.compactChatSession(activeSessionID)) {
          clearLocalCommandMessage();
        }
        return;
      case "task":
        if (activeHecateTaskID && onOpenTask) {
          onOpenTask(activeHecateTaskID, activeHecateRunID);
          clearLocalCommandMessage();
          return;
        }
        if (onNavigate) {
          onNavigate("runs");
          clearLocalCommandMessage();
          return;
        }
        settingsActions.setNoticeMessage("error", "Tasks are not available here.");
        return;
      case "project":
        if (!onOpenLinkedProject) {
          settingsActions.setNoticeMessage("error", "This command needs a linked project chat.");
          return;
        }
        onOpenLinkedProject();
        clearLocalCommandMessage();
        return;
      case "connections":
        if (!onNavigate) {
          settingsActions.setNoticeMessage("error", "Connections are not available here.");
          return;
        }
        onNavigate("connections");
        clearLocalCommandMessage();
        return;
    }
  }

  async function handleExternalAgentConfigChange(
    sessionID: string,
    configID: string,
    value: string | boolean,
  ): Promise<boolean> {
    if (sessionID !== "__draft__") {
      return chatActions.setChatConfigOption(sessionID, configID, value);
    }
    chat.actions.setAgentConfigOptions((current) =>
      updateDraftAgentConfigOptions(
        current.length > 0 ? current : draftAgentConfigOptions,
        configID,
        value,
      ),
    );
    return true;
  }

  function toggleModEnterMode() {
    setModEnterMode((v) => !v);
  }

  if (!composerVisible && !messageControlsVisible && !chatError && !selectedModelIssue) return null;

  return (
    <form
      ref={formRef}
      onSubmit={handleSubmit}
      style={{
        borderTop: "1px solid var(--border)",
        padding: "10px 12px",
        background: "var(--bg1)",
        flexShrink: 0,
      }}
    >
      {!composerVisible && chatError && (
        <div style={{ marginBottom: 8 }}>
          <ChatErrorPanel
            message={chatError}
            provider={runtimeHeaders?.provider}
            code={chatErrorCode}
            action={chatErrorAction}
            requestID={chatErrorRequestID}
            status={chatErrorStatus ?? undefined}
            traceID={chatErrorTraceID}
            onOpenTrace={onOpenTrace}
            diagnostic={chatDiagnostic}
          />
        </div>
      )}
      {!composerVisible && isHecateChat && selectedModelIssue && (
        <div style={{ marginBottom: 0 }}>
          <SelectedModelReadinessNotice
            issue={selectedModelIssue}
            disabled={composerRouteControlsDisabled}
            onOpenProviders={() => onNavigate?.("connections")}
            onUseSuggestedModel={(model) => {
              chatActions.selectProviderRoute("auto");
              onHecateModelChange(model);
            }}
          />
        </div>
      )}
      {messageControlsVisible && (
        <fieldset
          aria-label={
            isExternalAgentChat ? "External agent message controls" : "Hecate message controls"
          }
          disabled={composerRouteControlsDisabled}
          style={{
            maxWidth: 820,
            margin: composerVisible ? "0 auto 8px" : "0 auto",
            display: "flex",
            justifyContent: "flex-start",
            flexWrap: "wrap",
            gap: 6,
            border: 0,
            minWidth: 0,
            padding: 0,
          }}
        >
          {isExternalAgentChat ? (
            <>
              <ExternalAgentConfigControls
                session={externalConfigSession}
                disabled={composerRouteControlsDisabled}
                onChange={handleExternalAgentConfigChange}
                placement="composer"
              />
              {!activeExternalAgentID && (
                <details
                  style={{
                    width: "100%",
                    maxWidth: 520,
                    border: "1px solid var(--border)",
                    borderRadius: "var(--radius-sm)",
                    background: "var(--bg1)",
                    padding: "6px 8px",
                  }}
                >
                  <summary
                    style={{
                      cursor: "pointer",
                      color: "var(--t2)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                    }}
                  >
                    MCP servers{draftMCPServerCount > 0 ? ` x ${draftMCPServerCount}` : ""}
                  </summary>
                  <div style={{ marginTop: 8 }}>
                    <MCPServerEditor
                      entries={chat.state.agentMCPServers}
                      onChange={chat.actions.setAgentMCPServers}
                      label=""
                      includeApprovalPolicy={false}
                      compact
                    />
                  </div>
                </details>
              )}
            </>
          ) : hecateAgentModelLocked ? (
            <LockedHecateModelSnapshot
              provider={providerLabelForHecateChat(
                hecateChatProviderValue,
                settings.state.config?.providers,
                providerPresets,
                providers,
              )}
              model={hecateChatModelValue}
            />
          ) : (
            <>
              <HecateProviderConfigControl
                value={hecateChatProviderValue}
                onChange={(v) => chatActions.selectProviderRoute(v as typeof providerFilter)}
                options={hecateProviderOptions}
              />
              <HecateModelConfigControl
                value={model}
                onChange={onHecateModelChange}
                models={selectableModels}
                presets={providerPresets}
                configuredProviders={settings.state.config?.providers}
                runtimeProviders={providers}
                showProvider={false}
                disabledProviders={hecateDisabledProviderReasons}
              />
            </>
          )}
        </fieldset>
      )}
      {composerVisible && (
        <>
          {activeQueuedChatMessages.length > 0 && (
            <div
              aria-label="Queued messages"
              style={{
                maxWidth: 820,
                margin: "0 auto 8px",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                background: "var(--bg2)",
                padding: "7px 9px",
                display: "grid",
                gap: 6,
              }}
            >
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 8,
                }}
              >
                <span
                  style={{
                    color: "var(--t2)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    textTransform: "uppercase",
                    letterSpacing: "0.08em",
                  }}
                >
                  Queued next
                </span>
                <span style={{ color: "var(--t3)", fontSize: 11 }}>
                  {activeQueuedChatMessages.some(
                    (queued) => queued.delivery_storage_failed === true,
                  )
                    ? "browser storage failed — retry save or remove"
                    : activeQueuedChatMessages.some(
                          (queued) => queued.delivery_storage_conflict === "ready_replacement",
                        )
                      ? "queue id conflict — remove and submit again"
                      : activeQueuedChatMessages.some(
                            (queued) => queued.delivery_state === "submitting",
                          )
                        ? "sending queued item…"
                        : activeQueuedChatMessages.some(
                              (queued) => queued.delivery_state === "reconcile_required",
                            )
                          ? "check delivery status before retrying"
                          : activeQueuedChatMessages.some(
                                (queued) => queued.delivery_state === "retryable",
                              )
                            ? "review the failed item before retrying"
                            : "will send when the active run finishes"}
                </span>
              </div>
              {activeQueuedChatMessages.map((queued, index) => (
                <div
                  key={queued.id}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "auto minmax(0, 1fr) auto",
                    alignItems: "center",
                    gap: 8,
                    color: "var(--t0)",
                    fontSize: 12,
                  }}
                >
                  <span
                    style={{ color: "var(--teal)", fontFamily: "var(--font-mono)", fontSize: 10 }}
                  >
                    #{index + 1}
                  </span>
                  <textarea
                    aria-label={`Queued message ${index + 1}`}
                    className="queued-chat-message-input"
                    value={queued.content}
                    disabled={queued.delivery_state === "submitting"}
                    readOnly={queued.delivery_state === "reconcile_required"}
                    onChange={(event) =>
                      chat.actions.updateQueuedChatMessage(queued.id, event.target.value)
                    }
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && !event.shiftKey) event.preventDefault();
                    }}
                    rows={Math.min(4, Math.max(1, queued.content.split("\n").length))}
                    style={{
                      minWidth: 0,
                      width: "100%",
                      resize: "vertical",
                      borderRadius: "var(--radius-sm)",
                      color: "var(--t0)",
                      font: "inherit",
                      padding: "3px 6px",
                      outline: "none",
                    }}
                  />
                  <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
                    {queued.delivery_state === "reconcile_required" &&
                      queued.delivery_storage_conflict !== "ready_replacement" && (
                        <button
                          type="button"
                          className="btn btn-ghost btn-sm"
                          aria-label={`Check queued message ${index + 1} delivery status`}
                          disabled={chat.state.chatLoading || chatCancelling}
                          onClick={() => void chatActions.reconcileQueuedChatMessage(queued.id)}
                          style={{
                            padding: "2px 6px",
                            fontFamily: "var(--font-mono)",
                            fontSize: 10,
                          }}
                        >
                          check status
                        </button>
                      )}
                    {queued.delivery_state === "retryable" && (
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        aria-label={`Retry queued message ${index + 1}`}
                        onClick={() => chat.actions.retryQueuedChatMessage(queued.id)}
                        style={{
                          padding: "2px 6px",
                          fontFamily: "var(--font-mono)",
                          fontSize: 10,
                        }}
                      >
                        retry
                      </button>
                    )}
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      aria-label={`Remove queued message ${index + 1}`}
                      disabled={
                        queued.delivery_state === "submitting" ||
                        queued.delivery_storage_fenced === true
                      }
                      title={
                        queued.delivery_storage_fenced
                          ? "Check delivery status before removing a queued item observed from another tab or reload."
                          : undefined
                      }
                      onClick={() => chat.actions.removeQueuedChatMessage(queued.id)}
                      style={{
                        padding: "2px 6px",
                        fontFamily: "var(--font-mono)",
                        fontSize: 10,
                      }}
                    >
                      remove
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
          <div
            aria-label="Message composer"
            role="group"
            style={{
              maxWidth: 820,
              margin: "0 auto",
              position: "relative",
              border: `1px solid ${composerFocused ? "var(--teal)" : "var(--border)"}`,
              borderRadius: "var(--radius)",
              background: "var(--bg3)",
              boxShadow: composerFocused ? "0 0 0 1px var(--teal-bg)" : "none",
              transition: "border-color 0.1s, box-shadow 0.1s",
              opacity: composerInputDisabled ? 0.72 : undefined,
            }}
            onFocusCapture={() => setComposerFocused(true)}
            onBlurCapture={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                setComposerFocused(false);
              }
            }}
          >
            <div
              role="combobox"
              aria-controls={commandPickerVisible ? commandListboxID : undefined}
              aria-expanded={commandPickerVisible}
              aria-haspopup="listbox"
              aria-label="Message command picker"
              style={{ position: "relative" }}
            >
              {commandPickerVisible && (
                <div
                  id={commandListboxID}
                  role="listbox"
                  aria-label="Message commands"
                  style={{
                    position: "absolute",
                    left: 0,
                    right: 0,
                    bottom: "calc(100% + 6px)",
                    zIndex: 5,
                    border: "1px solid var(--border)",
                    borderRadius: "var(--radius-sm)",
                    background: "var(--bg2)",
                    boxShadow: "0 10px 28px rgba(0, 0, 0, 0.28)",
                    maxHeight: "min(60vh, 520px)",
                    overflowY: "auto",
                    overscrollBehavior: "contain",
                    scrollbarGutter: "stable",
                    touchAction: "pan-y",
                    padding: 4,
                    display: "grid",
                    gap: 2,
                  }}
                  onWheel={(event) => event.stopPropagation()}
                >
                  {commandSuggestions.map((command, index) => {
                    const commandText = messageCommandInsertion(command).trim();
                    const selected = index === activeCommandIndex;
                    return (
                      <div
                        key={`${command.kind}:${command.name}:${index}`}
                        ref={(node) => {
                          commandOptionRefs.current[index] = node;
                        }}
                        id={`${commandListboxID}-option-${index}`}
                        role="option"
                        aria-label={`Insert ${commandText} command`}
                        aria-selected={selected}
                        onMouseDown={(event) => event.preventDefault()}
                        onMouseEnter={() => setActiveCommandIndex(index)}
                        onClick={() => selectCommandSuggestion(command)}
                        style={{
                          width: "100%",
                          border: "none",
                          borderRadius: "var(--radius-sm)",
                          background: selected ? "var(--bg4)" : "transparent",
                          color: "var(--t0)",
                          cursor: "pointer",
                          display: "grid",
                          gridTemplateColumns: "minmax(84px, auto) minmax(0, 1fr) auto",
                          alignItems: "center",
                          gap: 10,
                          padding: "7px 8px",
                          textAlign: "left",
                        }}
                      >
                        <span
                          style={{
                            color: selected ? "var(--teal)" : "var(--t1)",
                            fontFamily: "var(--font-mono)",
                            fontSize: 12,
                            minWidth: 0,
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {commandText}
                        </span>
                        <span
                          style={{
                            color: "var(--t3)",
                            fontSize: 12,
                            minWidth: 0,
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {messageCommandDetail(command)}
                        </span>
                        {command.sourceLabel && (
                          <span
                            style={{
                              color: "var(--t3)",
                              fontFamily: "var(--font-mono)",
                              fontSize: 10,
                              textTransform: "uppercase",
                              letterSpacing: 0,
                              minWidth: 0,
                              overflow: "hidden",
                              textOverflow: "ellipsis",
                              whiteSpace: "nowrap",
                            }}
                          >
                            {command.sourceLabel}
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
              <textarea
                ref={textareaRef}
                aria-label="Message"
                aria-activedescendant={activeCommandOptionID}
                aria-controls={commandPickerVisible ? commandListboxID : undefined}
                aria-haspopup="listbox"
                aria-disabled={composerInputDisabled || undefined}
                readOnly={composerInputDisabled}
                value={message}
                onChange={(e) => handleMessageChange(e.target.value)}
                onKeyDown={handleKeyDown}
                onPaste={(event) => {
                  const files = Array.from(event.clipboardData.files);
                  if (files.length > 0) addPendingFiles(files);
                }}
                placeholder={
                  modEnterMode
                    ? `Message… (${modKey}+Enter to send)`
                    : "Message… (Shift+Enter for newline)"
                }
                rows={1}
                style={{
                  width: "100%",
                  background: "transparent",
                  border: "none",
                  borderRadius: "var(--radius) var(--radius) 0 0",
                  color: "var(--t0)",
                  fontFamily: "var(--font-sans)",
                  fontSize: 13,
                  boxSizing: "border-box",
                  padding: "11px 12px 7px",
                  outline: "none",
                  resize: "none",
                  lineHeight: 1.5,
                  minHeight: COMPOSER_TEXTAREA_MIN_HEIGHT,
                  overflowY: "hidden",
                  cursor: composerInputDisabled ? "wait" : undefined,
                }}
                onInput={(e) => adjustComposerTextareaHeight(e.target as HTMLTextAreaElement)}
              />
            </div>
            <div
              aria-label="Composer actions"
              role="group"
              style={{
                display: "flex",
                alignItems: "flex-end",
                gap: 6,
                minWidth: 0,
                padding: "0 7px 7px",
              }}
            >
              <ChatAttachmentDrafts
                attachments={chat.state.pendingChatAttachments}
                acceptance={attachmentAcceptance}
                enabled={attachmentsEnabled}
                disabledReason={attachmentsDisabledReason}
                error={attachmentSelectionError}
                compact
                onAddFiles={addPendingFiles}
                onRemove={removePendingFile}
              />
              <ChatDictationControl
                key={activeSessionID || "new-chat"}
                disabled={composerInputDisabled}
                onTranscript={insertDictationTranscript}
              />
              <span aria-hidden="true" style={{ flex: 1 }} />
              <button
                type="submit"
                aria-label={
                  attachmentTurnInFlight
                    ? "Wait for attachment response"
                    : queueingMessage
                      ? "Queue message"
                      : "Send message"
                }
                disabled={sendDisabled}
                title={
                  attachmentTurnInFlight
                    ? "Wait for the attachment response before sending this message"
                    : queueingMessage
                      ? "Queue this message after the active run finishes"
                      : messageSendBlocked
                        ? "Complete chat setup before sending"
                        : "Send message"
                }
                style={{
                  width: 28,
                  height: 28,
                  flexShrink: 0,
                  borderRadius: "var(--radius-sm)",
                  background: !sendDisabled ? "var(--teal)" : "var(--bg4)",
                  border: "none",
                  cursor: !sendDisabled ? "pointer" : "default",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  transition: "background 0.1s",
                  color: !sendDisabled ? "var(--bg0)" : "var(--t3)",
                }}
              >
                <Icon d={Icons.send} size={14} />
              </button>
            </div>
          </div>
          {composerNoticeVisible && (
            <div
              aria-label="Composer notices"
              style={{ maxWidth: 820, margin: "6px auto 0", display: "grid", gap: 6 }}
            >
              {composerRepair && (
                <ChatSetupRepairNotice
                  repair={composerRepair}
                  onAction={(repair) => {
                    if (repair.action === "choose_workspace") {
                      void chooseWorkspace();
                    } else if (repair.action === "open_agent_setup") {
                      openExternalAgentSetup();
                    } else if (repair.action === "open_connections") {
                      onNavigate?.("connections");
                    }
                  }}
                />
              )}
              {chatError && (
                <ChatErrorPanel
                  message={chatError}
                  provider={runtimeHeaders?.provider}
                  code={chatErrorCode}
                  action={chatErrorAction}
                  requestID={chatErrorRequestID}
                  status={chatErrorStatus ?? undefined}
                  traceID={chatErrorTraceID}
                  onOpenTrace={onOpenTrace}
                  diagnostic={chatDiagnostic}
                />
              )}
              {isHecateChat && selectedModelIssue && (
                <SelectedModelReadinessNotice
                  issue={selectedModelIssue}
                  disabled={composerRouteControlsDisabled}
                  onOpenProviders={() => onNavigate?.("connections")}
                  onUseSuggestedModel={(model) => {
                    chatActions.selectProviderRoute("auto");
                    onHecateModelChange(model);
                  }}
                />
              )}
            </div>
          )}
          {isAgentChat && rawChatCancelling && (
            <div
              aria-atomic="true"
              aria-live="polite"
              role="status"
              style={{
                maxWidth: 820,
                margin: "6px auto 0",
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
              }}
            >
              {chatCancelling
                ? stoppingStatusText
                : "Another chat is stopping. Wait for that request to finish."}
            </div>
          )}
          <div
            style={{
              maxWidth: 820,
              margin: "3px auto 0",
              display: "flex",
              alignItems: "center",
              gap: 10,
              justifyContent: "space-between",
              minHeight: 22,
            }}
          >
            <span
              aria-label={agentBusy ? "Active run status" : undefined}
              style={{
                minWidth: 0,
                color: agentBusy ? "var(--amber)" : "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                display: "inline-flex",
                alignItems: "center",
                gap: 8,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
                {composerStatusText}
              </span>
              {agentBusy && onOpenTask && activeHecateTaskID && (
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  onClick={() => onOpenTask(activeHecateTaskID, activeHecateRunID)}
                  style={{
                    flexShrink: 0,
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    padding: "2px 6px",
                    color: "var(--amber)",
                  }}
                >
                  Open task
                </button>
              )}
              {(agentBusy || chatCancelling) &&
                (activeRunCancellationAvailable || chatCancelling) && (
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    aria-label={chatCancelling ? stoppingStatusText : stopActionLabel}
                    title={
                      chatCancelling
                        ? stoppingStatusText
                        : anotherChatCancelling
                          ? "Another chat is stopping. Wait for that request to finish."
                          : stopActionLabel
                    }
                    onClick={chatActions.cancelAgentChat}
                    disabled={rawChatCancelling}
                    style={{
                      flexShrink: 0,
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      padding: "2px 6px",
                      color: "var(--danger)",
                    }}
                  >
                    Stop
                  </button>
                )}
              {projectProposalAvailable && onDraftProjectProposal && (
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  aria-label="Draft Project Assistant proposal from message"
                  disabled={projectProposalDrafting}
                  title="Draft a Project Assistant proposal from this message"
                  onClick={() => onDraftProjectProposal()}
                  style={{
                    flexShrink: 0,
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    padding: "2px 6px",
                    color: "var(--teal)",
                  }}
                >
                  <Icon d={Icons.projects} size={12} />
                  {projectProposalDrafting ? "Drafting..." : "Draft proposal"}
                </button>
              )}
            </span>
            <button
              type="button"
              onClick={toggleModEnterMode}
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--t3)",
                background: "none",
                border: "none",
                cursor: "pointer",
                padding: 0,
              }}
            >
              {modEnterMode ? `${modKey}+↵ to send` : "↵ to send"}
            </button>
          </div>
        </>
      )}
    </form>
  );
}

function adjustComposerTextareaHeight(textarea: HTMLTextAreaElement) {
  const maxHeight = composerTextareaMaxHeight(textarea);
  const borderHeight =
    numericStyleValue(textarea, "borderTopWidth") +
    numericStyleValue(textarea, "borderBottomWidth");
  textarea.style.height = "auto";
  const nextHeight = Math.min(
    Math.max(textarea.scrollHeight + borderHeight, COMPOSER_TEXTAREA_MIN_HEIGHT),
    maxHeight,
  );
  textarea.style.height = `${nextHeight}px`;
  textarea.style.overflowY = textarea.scrollHeight + borderHeight > maxHeight ? "auto" : "hidden";
}

function composerTextareaMaxHeight(textarea: HTMLTextAreaElement) {
  const style = window.getComputedStyle(textarea);
  const fontSize = Number.parseFloat(style.fontSize) || 13;
  const parsedLineHeight = Number.parseFloat(style.lineHeight);
  const lineHeight = Number.isFinite(parsedLineHeight)
    ? style.lineHeight.endsWith("px")
      ? parsedLineHeight
      : parsedLineHeight * fontSize
    : fontSize * 1.5;
  const paddingHeight =
    numericStyleValue(textarea, "paddingTop") + numericStyleValue(textarea, "paddingBottom");
  const borderHeight =
    numericStyleValue(textarea, "borderTopWidth") +
    numericStyleValue(textarea, "borderBottomWidth");
  return Math.ceil(lineHeight * COMPOSER_TEXTAREA_MAX_LINES + paddingHeight + borderHeight);
}

function numericStyleValue(element: HTMLElement, property: ComposerTextareaNumericStyle) {
  return Number.parseFloat(window.getComputedStyle(element)[property]) || 0;
}

// Picker parsing and submit routing are intentionally separate: the picker
// reads an in-progress command token, while submit routing needs full args.
function messageCommandQuery(message: string): string | null {
  if (!message.startsWith("/")) return null;
  const query = message.slice(1);
  if (/\s/.test(query)) return null;
  return query;
}

function externalAgentCommandName(command: ChatAvailableCommandRecord) {
  return command.name.trim().replace(/^\/+/, "");
}

function hecateMessageCommandSourceLabel(name: HecateMessageCommandName) {
  return HECATE_PROJECT_COMMAND_NAMES.has(name) ? "Project" : "Hecate";
}

type HecateMessageCommandAvailability = {
  projectProposalAvailable: boolean;
  workspaceChangesAvailable: boolean;
  chatSettingsAvailable: boolean;
  compactAvailable: boolean;
  taskAvailable: boolean;
  projectAvailable: boolean;
  connectionsAvailable: boolean;
};

function availableHecateMessageCommands(availability: HecateMessageCommandAvailability) {
  const out: Array<{ name: HecateMessageCommandName; description: string; inputHint?: string }> =
    [];
  const push = (name: HecateMessageCommandName) =>
    out.push({ name, ...HECATE_MESSAGE_COMMANDS[name] });

  if (availability.projectProposalAvailable) {
    push("proposal");
    push("plan");
    push("work");
    push("handoff");
    push("review");
  }
  if (availability.workspaceChangesAvailable) push("diff");
  if (availability.chatSettingsAvailable) {
    push("model");
    push("settings");
    push("status");
    push("context");
  }
  if (availability.compactAvailable) push("compact");
  if (availability.taskAvailable) push("task");
  if (availability.projectAvailable) push("project");
  if (availability.connectionsAvailable) push("connections");

  return out;
}

function messageCommandInsertion(command: MessageCommandSuggestion) {
  const name = command.name;
  return name ? `/${name} ` : "";
}

function messageCommandDetail(command: MessageCommandSuggestion) {
  const description = command.description?.trim();
  if (description) return description;
  return command.inputHint?.trim() ?? "";
}

type ParsedHecateMessageCommand = {
  name: string;
  args: string;
};

function parseHecateMessageCommand(message: string): ParsedHecateMessageCommand | null {
  if (!message.startsWith("/")) return null;
  const match = message.match(/^\/([A-Za-z][\w-]*)(?:\s+([\s\S]*))?$/);
  if (!match) return null;
  return {
    name: match[1].toLowerCase(),
    args: (match[2] ?? "").trim(),
  };
}

function hecateCommandDefinition(name: string) {
  return HECATE_MESSAGE_COMMANDS[name as HecateMessageCommandName];
}

function projectAssistantRequestForHecateCommand(name: string, request: string) {
  switch (name) {
    case "work":
      return `Create project work from this chat request:\n\n${request}`;
    case "handoff":
      return `Draft a project handoff from this chat request:\n\n${request}`;
    case "review":
      return `Draft a project review request from this chat request:\n\n${request}`;
    default:
      return request;
  }
}

export function ChatErrorPanel({
  message,
  provider,
  code,
  action,
  requestID,
  status,
  traceID,
  onOpenTrace,
  diagnostic,
}: {
  message: string;
  provider?: string;
  code?: string;
  action?: string;
  requestID?: string;
  status?: number;
  traceID?: string;
  onOpenTrace?: (requestID: string) => void;
  diagnostic: ReturnType<typeof describeGatewayError>;
}) {
  const label = formatErrorCode(code, status);
  const recommendedAction = action || diagnostic?.action || "";
  if (!diagnostic) {
    return <InlineError message={`${provider ? `[${provider}] ` : ""}${message}`} />;
  }
  const tone = diagnostic.tone === "warning" ? "warning" : "danger";
  const vars = chatErrorPanelToneVars(tone);
  const showTechnicalLabel = code !== "chat.workspace_required";

  return (
    <div
      role={tone === "warning" ? "status" : "alert"}
      aria-live={tone === "warning" ? "polite" : undefined}
      style={{
        border: `1px solid ${vars.border}`,
        background: vars.bg,
        borderRadius: "var(--radius)",
        padding: "9px 11px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: vars.fg }}>{diagnostic.title}</span>
        {label && showTechnicalLabel && (
          <span
            style={{
              marginLeft: "auto",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: vars.fg,
            }}
          >
            {label}
          </span>
        )}
      </div>
      <div style={{ fontSize: 12, color: "var(--t0)", lineHeight: 1.45 }}>{message}</div>
      {recommendedAction && (
        <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.45, marginTop: 5 }}>
          {provider ? `${provider}: ` : ""}
          {recommendedAction}
        </div>
      )}
      {(requestID || traceID) && (
        <div
          style={{ marginTop: 7, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}
        >
          {requestID && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              request <span style={{ color: "var(--t1)" }}>{compactID(requestID, [], 10)}</span>
            </span>
          )}
          {traceID && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              trace <span style={{ color: "var(--t1)" }}>{compactID(traceID, [], 10)}</span>
            </span>
          )}
          {requestID && onOpenTrace && (
            <button
              type="button"
              onClick={() => onOpenTrace(requestID)}
              style={{
                border: `1px solid ${vars.border}`,
                background: "transparent",
                color: vars.fg,
                borderRadius: 999,
                padding: "2px 8px",
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                cursor: "pointer",
              }}
            >
              Open trace
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function chatErrorPanelToneVars(tone: "danger" | "warning") {
  if (tone === "warning") {
    return {
      bg: "rgba(245, 191, 79, 0.055)",
      fg: "var(--amber)",
      border: "rgba(245, 191, 79, 0.28)",
    };
  }
  return {
    bg: "var(--red-bg)",
    fg: "var(--red)",
    border: "var(--red-border)",
  };
}

export function ChatSetupRepairNotice({
  repair,
  actionBusy = false,
  actionDisabled = false,
  actionTitle,
  onAction,
}: {
  repair: ChatSetupRepairState;
  actionBusy?: boolean;
  actionDisabled?: boolean;
  actionTitle?: string;
  onAction: (repair: ChatSetupRepairState) => void;
}) {
  return (
    <ChatNoticeInline
      tone={repair.tone}
      title={repair.title}
      message={repair.message}
      action={repair.actionLabel}
      actionBusy={actionBusy}
      actionBusyLabel="Saving..."
      actionDisabled={actionDisabled}
      actionTitle={actionTitle}
      onAction={() => onAction(repair)}
    />
  );
}

export function SelectedModelReadinessNotice({
  issue,
  compact = false,
  disabled = false,
  onOpenProviders,
  onUseSuggestedModel,
}: {
  issue: SelectedModelIssue;
  compact?: boolean;
  disabled?: boolean;
  onOpenProviders?: () => void;
  onUseSuggestedModel?: (model: string) => void;
}) {
  return (
    <div
      style={{
        margin: compact ? "14px auto 0" : "0 auto",
        maxWidth: compact ? 560 : 820,
        border: "1px solid rgba(245, 191, 79, 0.32)",
        borderRadius: "var(--radius)",
        background: "rgba(245, 191, 79, 0.06)",
        padding: 12,
        textAlign: "left",
      }}
    >
      {!compact && (
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "space-between",
            gap: 12,
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 12, fontWeight: 700, color: "var(--amber)", marginBottom: 4 }}>
              {issue.title}
            </div>
            <div style={{ fontSize: 12, color: "var(--t2)", lineHeight: 1.5 }}>{issue.message}</div>
          </div>
          {onOpenProviders && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={onOpenProviders}
              style={{ flexShrink: 0 }}
            >
              Open Connections
            </button>
          )}
          {issue.suggestedModel && onUseSuggestedModel && (
            <button
              className="btn btn-primary btn-sm"
              type="button"
              disabled={disabled}
              onClick={() => onUseSuggestedModel(issue.suggestedModel!)}
              style={{ flexShrink: 0 }}
            >
              Use {issue.suggestedModel}
            </button>
          )}
        </div>
      )}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))",
          gap: 8,
          marginTop: 10,
        }}
      >
        {selectedModelNoticeDetails(issue.details, compact).map((detail) => (
          <InfoChip key={detail.label} label={detail.label} value={detail.value} />
        ))}
      </div>
      {compact ? (
        <>
          <ul
            style={{
              margin: "10px 0 0",
              paddingLeft: 18,
              color: "var(--t3)",
              fontSize: 11,
              lineHeight: 1.55,
            }}
          >
            {issue.steps.slice(0, 2).map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ul>
          {issue.suggestedModel && onUseSuggestedModel && (
            <button
              className="btn btn-primary btn-sm"
              type="button"
              disabled={disabled}
              onClick={() => onUseSuggestedModel(issue.suggestedModel!)}
              style={{ marginTop: 10 }}
            >
              Use {issue.suggestedModel}
            </button>
          )}
        </>
      ) : (
        <ul
          style={{
            margin: "10px 0 0",
            paddingLeft: 18,
            color: "var(--t3)",
            fontSize: 11,
            lineHeight: 1.55,
          }}
        >
          {issue.steps.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function selectedModelNoticeDetails(
  details: SelectedModelIssue["details"],
  compact: boolean,
): SelectedModelIssue["details"] {
  if (!compact) {
    return details;
  }
  const priorityLabels = new Set([
    "Selected model",
    "Provider route",
    "Discovered models",
    "Health",
    "Blocked by",
    "Last error",
  ]);
  const selected = details.filter((detail) => priorityLabels.has(detail.label));
  return selected.length > 0 ? selected : details;
}

function InfoChip({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg3)",
        padding: "7px 8px",
        minWidth: 0,
      }}
    >
      <div
        style={{
          fontSize: 10,
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          textTransform: "uppercase",
          letterSpacing: "0.04em",
        }}
      >
        {label}
      </div>
      <div
        title={value}
        style={{
          marginTop: 3,
          fontSize: 11,
          color: "var(--t1)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {value}
      </div>
    </div>
  );
}

function providerLabelForHecateChat(
  providerID: string,
  configuredProviders: ConfiguredProviderRecord[] | undefined,
  providerPresets: ProviderPresetRecord[],
  providers: ProviderRecord[],
): string {
  if (!providerID || providerID === "auto") {
    return "Select provider";
  }
  return providerDisplayName(providerID, configuredProviders, providerPresets, providers);
}

function updateDraftAgentConfigOptions(
  options: ChatConfigOptionRecord[],
  configID: string,
  value: string | boolean,
): ChatConfigOptionRecord[] {
  return options.map((option) => {
    if (option.id !== configID) {
      return option;
    }
    if (typeof value === "boolean") {
      return { ...option, current_bool: value };
    }
    return { ...option, current_value: value };
  });
}

export function repairActionIcon(repair: ChatSetupRepairState) {
  switch (repair.action) {
    case "choose_workspace":
      return Icons.folder;
    case "open_agent_setup":
      return Icons.terminal;
    case "use_suggested_model":
      return Icons.model;
    case "open_connections":
      return Icons.connections;
  }
  return Icons.providers;
}

export function compactID(id: string, prefixes: string[], length: number): string {
  const trimmed = id.trim();
  const withoutPrefix = prefixes.reduce(
    (current, prefix) => (current.startsWith(prefix) ? current.slice(prefix.length) : current),
    trimmed,
  );
  return withoutPrefix.slice(0, length);
}

export function insertTranscriptAtSelection(
  current: string,
  transcript: string,
  selectionStart: number,
  selectionEnd: number,
): { value: string; cursor: number } {
  const start = Math.max(0, Math.min(current.length, selectionStart));
  const end = Math.max(start, Math.min(current.length, selectionEnd));
  const before = current.slice(0, start);
  const after = current.slice(end);
  let insertion = transcript.trim();
  if (insertion === "") return { value: current, cursor: start };
  if (before !== "" && !/\s$/.test(before) && !/^[,.;:!?)}\]]/.test(insertion)) {
    insertion = ` ${insertion}`;
  }
  if (after !== "" && !/^\s/.test(after) && !/[({[]$/.test(insertion)) {
    insertion = `${insertion} `;
  }
  return {
    value: before + insertion + after,
    cursor: before.length + insertion.length,
  };
}
