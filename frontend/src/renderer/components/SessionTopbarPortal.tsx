import { createContext, useCallback, useContext, useMemo, useRef, useState, type ComponentProps, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../lib/utils";

type SessionTopbarContextValue = {
	host: HTMLDivElement | null;
	setHost: (host: HTMLDivElement | null) => void;
};

const SessionTopbarContext = createContext<SessionTopbarContextValue | undefined>(undefined);

export function SessionTopbarProvider({ children }: { children: ReactNode }) {
	const [host, setHost] = useState<HTMLDivElement | null>(null);
	const value = useMemo(() => ({ host, setHost }), [host]);

	return <SessionTopbarContext.Provider value={value}>{children}</SessionTopbarContext.Provider>;
}

export function SessionTopbarHost({ className, ref, ...props }: ComponentProps<"div">) {
	const context = useContext(SessionTopbarContext);
	if (!context) throw new Error("SessionTopbarHost must be used within SessionTopbarProvider");

	const { setHost } = context;
	// The ref callback must keep a stable identity. An inline callback is
	// recreated on every render, so React detached it with a setHost(null) and
	// reattached it with a setHost(node) inside every commit of the host div.
	// Under live session fact updates those commit-phase dispatches chained
	// past React's update-depth limit and crashed session_detail with React
	// error #185 (fork issue #346). The caller's ref rides in a holder, so even
	// an unstable forwarded ref cannot bring the churn back.
	const forwardedRef = useRef(ref);
	forwardedRef.current = ref;
	const attachRef = useCallback(
		(element: HTMLDivElement | null) => {
			setHost(element);
			const forwarded = forwardedRef.current;
			if (typeof forwarded === "function") forwarded(element);
			else if (forwarded) forwarded.current = element;
		},
		[setHost],
	);

	return (
		<div
			className={cn("workspace-topbar-container", className)}
			ref={attachRef}
			{...props}
		/>
	);
}

export function SessionTopbarPortal({ children }: { children: ReactNode }) {
	const context = useContext(SessionTopbarContext);
	if (!context) return children;
	if (!context.host) return null;
	return createPortal(children, context.host);
}
