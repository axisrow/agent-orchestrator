// Real composer components and production CSS, without the shell or daemon.
import { type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { ChatComposer } from "../../src/renderer/components/chat/ChatComposer";
import { writeChatComposerText } from "../../src/renderer/lib/chat-drafts";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
const theme = params.get("theme") === "light" ? "light" : "dark";
const translucent = params.get("transparency") === "true";
document.documentElement.dataset.theme = theme;
document.documentElement.dataset.windowTranslucent = String(translucent);
const noop = () => {};
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

// The Send sample asserts an enabled send and reads back this text via
// selectText(). An empty draft keeps Send disabled by design, so seed the
// composer's own persisted-draft store before it mounts.
const SEND_SAMPLE_SCOPE = "chat-contrast-send";
writeChatComposerText(SEND_SAMPLE_SCOPE, "Selected text stays readable");

function Sample({ name, children }: { name: string; children: ReactNode }) {
	return <section aria-label={name}><h2 className="mb-2 text-sm text-muted-foreground">{name}</h2>{children}</section>;
}

createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={client}><I18nextProvider i18n={appI18n}><TooltipProvider>
		<main className="app-shell-root min-h-screen p-6 text-foreground">
			<h1 className="mb-1 text-lg">Chat controls · {theme} · transparency {translucent ? "on" : "off"}</h1>
			<p className="mb-6 text-sm text-muted-foreground">Isolated production components; no Electron window or daemon.</p>
			<div className="grid grid-cols-2 gap-6">
				<Sample name="Send"><ChatComposer onSend={noop} draftSessionId={SEND_SAMPLE_SCOPE} /></Sample>
				<Sample name="Stop"><ChatComposer onSend={noop} willQueue onInterrupt={noop} /></Sample>
				<Sample name="Continue"><ChatComposer onSend={noop} onContinue={noop} /></Sample>
			</div>
		</main>
	</TooltipProvider></I18nextProvider></QueryClientProvider>,
);
