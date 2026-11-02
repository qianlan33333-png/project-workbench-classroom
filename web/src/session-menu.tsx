import type { ReactNode } from "react";
import { MoreHorizontal } from "lucide-react";
import { ActionMenu } from "./action-menu";

export function SessionMenu({
  label,
  context,
  children,
}: {
  label: string;
  context: string;
  children: ReactNode;
}) {
  return (
    <ActionMenu
      label={label}
      context={context}
      triggerContent={<MoreHorizontal size={15} />}
      triggerClassName="session-menu-trigger"
      panelClassName="session-menu-popover"
    >
      {children}
    </ActionMenu>
  );
}
