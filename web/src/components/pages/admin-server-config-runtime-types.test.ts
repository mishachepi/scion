/**
 * The runtime Type select offers exactly the types the settings schema
 * accepts for `runtimes.<name>.type`, so every runtime the broker can build
 * can be chosen on the page.
 */
import { describe, it, expect } from 'vitest';
import settingsSchema from '../../../../pkg/config/schemas/settings-v1.schema.json';
import { RUNTIME_TYPES } from './admin-server-config.js';

describe('admin server config runtime types', () => {
  it('match the settings schema enum', () => {
    const enumValues = (
      settingsSchema as unknown as {
        $defs: { runtimeConfig: { properties: { type: { enum: string[] } } } };
      }
    ).$defs.runtimeConfig.properties.type.enum;
    expect([...RUNTIME_TYPES].sort()).toEqual([...enumValues].sort());
  });
});
