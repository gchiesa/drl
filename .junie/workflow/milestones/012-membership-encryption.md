# 012-membership-encryption-and-rotation.md

## Goal

Implement secure cluster communication with support for zero-downtime key rotation. This uses a `Keyring` to allow nodes
to transition between encryption keys without losing connectivity.

## Requirements

### 1. Configuration: From Secret to Keyring

* **KDL Schema Update**: Support a list of keys instead of a single string.
    ```kdl
    membership {
        // The first key in the list is ALWAYS the Primary (Encryption) key
        // Subsequent keys are for Decryption only (to support rotation)
        secret-keys [
            "PrimaryLatestKey_32Bytes________________", 
            "OldLegacyKey_32Bytes____________________"
        ]
    }
    ```
* **Environment Variables**:
    * `MEMBERLIST_PRIMARY_KEY`: Sets the primary encryption key.
    * `MEMBERLIST_SECONDARY_KEYS`: A comma-separated list of keys allowed for decryption.

### 2. Implementation: memberlist.Keyring

* Update `internal/membership` to initialize a `memberlist.Keyring` using `memberlist.NewKeyring(keys, primaryKey)`.
* Pass this keyring into the `memberlist.Config.Keyring` field.
* **Validation**: Ensure all keys provided are valid AES lengths (16, 24, or 32 bytes).

### 3. Key Rotation Test Suite

Create a specific integration test that simulates the rotation:

1. **Initial State**: Node A and B share `Key_1`. Communication is active.
2. **Staging**: Add `Key_2` to Node A as secondary. Add `Key_2` to Node B as primary (with `Key_1` as secondary).
3. **Verification**: Verify that Node A can still read Node B's messages (using its secondary `Key_2`) and vice versa.
4. **Final**: Switch Node A to primary `Key_2`. Verify perfect communication.

## Important requirements

1. **Security**: Providing an empty key list (if encryption was previously enabled) should trigger a "Degraded Security"
   warning in logs.
2. If no key is specified in the configuration the encryption is disabled. This should still be reported in the logs  
