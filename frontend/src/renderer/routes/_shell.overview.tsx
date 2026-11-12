import { createFileRoute } from "@tanstack/react-router";
import { OverviewPage } from "../components/OverviewPage";

export const Route = createFileRoute("/_shell/overview")({ component: OverviewPage });
