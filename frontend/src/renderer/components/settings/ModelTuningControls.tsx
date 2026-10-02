import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { isConcreteModelID } from "../../lib/agent-model-choices";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

type Model = components["schemas"]["AgentModelInfo"];

// ponytail: ladder for gateways that don't advertise capabilities; revisit when providers report efforts.
export const FALLBACK_EFFORTS = ["low", "medium", "high"];

export type EffortChoices = {
	options: string[];
	defaultEffort: string;
	/** True when the ladder is a fallback because the provider did not report efforts. */
	unverified: boolean;
};

export function effortChoices(selected: Model | undefined, hasModel: boolean): EffortChoices {
	const options = (selected?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default");
	if (options.length) {
		const defaultEffort = selected?.defaultEffort ?? "";
		return { options, defaultEffort: options.includes(defaultEffort) ? defaultEffort : "", unverified: false };
	}
	return { options: hasModel ? FALLBACK_EFFORTS : [], defaultEffort: "", unverified: hasModel };
}

export type ModelTuningControlsProps = {
	models?: Model[];
	model: string;
	effort: string;
	onEffortChange: (value: string) => void;
	onEffortReset?: (value: string) => void;
	onValidityChange?: (valid: boolean) => void;
	variant: "settings" | "composer";
	roleLabel?: string;
	disabled?: boolean;
};

export function useModelTuning(props: Omit<ModelTuningControlsProps, "variant" | "disabled">) {
	const {
		models,
		model,
		effort,
		onEffortChange,
		onEffortReset = onEffortChange,
		onValidityChange,
	} = props;
	const previousModel = useRef(model);
	const previousValidity = useRef<boolean | undefined>(undefined);
	const concreteModel = isConcreteModelID(model) ? model : "";
	const selected =
		(concreteModel ? models?.find((item) => item.id === concreteModel) : undefined) ??
		(concreteModel === "" ? models?.find((item) => item.isDefault && isConcreteModelID(item.id)) : undefined);
	const capabilitiesKnown = models !== undefined;
	const advertised = (selected?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default");
	// Lenient when the provider doesn't advertise efforts (gateways, off-catalog
	// models): a saved value can't be proven invalid, so keep it and don't warn.
	const invalidEffort = Boolean(
		effort && effort.toLowerCase() !== "default" && capabilitiesKnown && advertised.length && !advertised.includes(effort),
	);

	useEffect(() => {
		if (previousModel.current === model) return;
		if (!capabilitiesKnown) return;
		previousModel.current = model;
		if (effort && effort.toLowerCase() !== "default" && advertised.length && !advertised.includes(effort)) onEffortReset("");
	}, [capabilitiesKnown, effort, model, onEffortReset, advertised]);

	useEffect(() => {
		const valid = !invalidEffort;
		if (previousValidity.current === valid) return;
		previousValidity.current = valid;
		onValidityChange?.(valid);
	}, [invalidEffort, onValidityChange]);
	return { selected, invalidEffort };
}

export function ModelTuningControls(props: ModelTuningControlsProps) {
	const { t } = useTranslation();
	const { effort, model, onEffortChange, variant, roleLabel, disabled } = props;
	const { selected, invalidEffort } = useModelTuning(props);
	const concreteModel = isConcreteModelID(model) ? model : "";
	const choices = effortChoices(selected, Boolean(concreteModel));
	const prefix = roleLabel ? `${roleLabel} ` : "";
	const warning = invalidEffort
		? t("settings.models.unsupportedTuning", { role: roleLabel ? `${roleLabel} ` : "" })
		: null;
	if (!selected && !concreteModel) {
		return warning && variant === "settings" ? (
			<p role="alert" className="px-1 text-xs leading-row text-warning">{warning}</p>
		) : null;
	}
	const effortOptions = choices.options;
	const explicitEffort = effort.toLowerCase() === "default" ? "" : effort;
	const effectiveEffort = explicitEffort || choices.defaultEffort;
	const unverifiedHint = choices.unverified ? t("settings.models.effortUnverified") : null;
	const effortControl = effortOptions.length ? (
		<SettingsOptionMenu
			aria-label={`${prefix}${t("settings.models.effort")}`}
			value={effectiveEffort}
			placeholder={t("settings.models.effortNotReported")}
			disabled={disabled}
			options={effortOptions.map((value) => ({ value, label: value }))}
			onChange={onEffortChange}
			triggerClassName={variant === "composer" ? "composer-chip composer-toolbar-option" : "justify-end"}
		/>
	) : null;
	if (!effortControl) return null;
	if (variant === "composer") {
		return effortControl;
	}
	return (
		<>
			{effortControl ? <SettingsRow label={`${prefix}${t("settings.models.effort")}`}>{effortControl}</SettingsRow> : null}
			{unverifiedHint ? <p className="px-1 text-xs leading-row text-settings-muted">{unverifiedHint}</p> : null}
			{warning ? <p role="alert" className="px-1 text-xs leading-row text-warning">{warning}</p> : null}
		</>
	);
}
