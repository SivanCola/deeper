import type { ProviderView } from "./types";
import { matchingModelKey, modelCapabilityForModel } from "./providerImageInput";

export type ProviderModelVisionCapability = "supported" | "unsupported" | "unknown";

/**
 * Returns the models known to accept images from metadata and model overrides.
 */
export function providerVisionModelsForView(
  provider: Pick<ProviderView, "models" | "modelOverrides" | "modelCapabilities">,
  models = provider.models,
): string[] {
  return models.filter((model) => {
    const metadata = modelCapabilityForModel(provider.modelCapabilities, model);
    if (metadata?.imageInputEnableAllowed === false) return false;
    const key = matchingModelKey((provider.modelOverrides ?? []).map((item) => item.model), model);
    const override = provider.modelOverrides?.find((item) => item.model === key)?.vision;
    if (override !== undefined && override !== null) return override;
    if (metadata) return metadata.state === "supported";
    return false;
  });
}

/** Returns a conservative read-only capability label for one model. */
export function providerModelVisionCapability(
  provider: Pick<ProviderView, "modelCapabilities" | "visionCapability">,
  model: string,
  visionModels: string[],
): ProviderModelVisionCapability {
  const metadata = modelCapabilityForModel(provider.modelCapabilities, model);
  if (metadata) return metadata.state === "supported" ? "supported" : metadata.state === "unknown" ? "unknown" : "unsupported";
  if (visionModels.includes(model)) return "supported";
  if (provider.visionCapability === "unsupported") return "unsupported";
  return "unknown";
}
