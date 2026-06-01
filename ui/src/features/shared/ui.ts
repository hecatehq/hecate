// shared/ui.tsx is a barrel for the shared visual primitives. Each
// component lives in its own file now; this module re-exports them so
// existing call sites can keep importing from "../shared/ui" without
// being rewritten. New code should import directly from the
// per-component files for better tree-shaking and tighter boundaries.

export { Icon, Icons } from "./Icons";
export { Badge, Dot, Toggle, CopyBtn, InlineError, CodeBlock } from "./Atoms";
export { CopyableID } from "./CopyableID";
export { ChipInput } from "./ChipInput";
export type { ChipOption } from "./ChipInput";
export { DropdownPicker } from "./DropdownPicker";
export type { DropdownPickerOption } from "./DropdownPicker";
export { BrandAvatar } from "./BrandAvatar";
export { SlideOver, Modal, ConfirmModal } from "./Overlays";
export { ModelPicker } from "./ModelPicker";
export { ProviderPicker } from "./ProviderPicker";
export type { ProviderOption } from "./ProviderPicker";
export { AgentAdapterPicker } from "./AgentAdapterPicker";
