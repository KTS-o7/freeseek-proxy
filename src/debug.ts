import "dotenv/config";
import jsSha3 from "js-sha3";
const sha3_512 = jsSha3.sha3_512;

const DEEPSEEK_BASE = "https://chat.deepseek.com";
const AUTH_TOKEN = process.env.DEEPSEEK_AUTH_TOKEN ?? "";
const COOKIES = process.env.DEEPSEEK_COOKIES ?? "";

// Verify algorithm with the known solution from original curl
async function verifyAlgorithm() {
  console.log("=== Verifying algorithm ===");
  
  // From original curl, the challenge is the SHA3-512 hash
  const challenge = "dc2d26fe62e32114c3ebb2113ad47d2c094a1336e07654d5ec14f02a3c764d1";
  const salt = "f4934256b6c7c47e0589";
  const answer = 56926;
  const signature = "23aaf9bb895d4aec373651a0e00cb69539cc909ec8a73779a991fc9e4615c4c3";
  
  // The WASM compute_hash function does: sha3_512(challenge + prefix)
  // where prefix = salt_expireat_answer (with actual expire_at from server)
  
  // First, get the actual challenge from server to get expire_at
  const powResp = await fetch(
    `${DEEPSEEK_BASE}/api/v0/chat/create_pow_challenge`,
    {
      method: "POST",
      headers: {
        authorization: `Bearer ${AUTH_TOKEN}`,
        "content-type": "application/json",
        cookie: COOKIES,
        origin: DEEPSEEK_BASE,
      },
      body: JSON.stringify({ target_path: "/api/v0/chat/completion" }),
    }
  );
  const powData = await powResp.json();
  const challengeData = powData?.data?.biz_data?.challenge;
  console.log("Challenge data:", JSON.stringify(challengeData, null, 2));

  // Test the WASM approach on a server-provided challenge
  const serverChallenge = challengeData.challenge;
  const serverSalt = challengeData.salt;
  const serverDifficulty = challengeData.difficulty;
  const serverExpireAt = challengeData.expire_at;
  const serverSignature = challengeData.signature;
  
  const prefix = `${serverSalt}_${serverExpireAt}_`;
  console.log(`\nPrefix: "${prefix}"`);
  
  // Try a few answers to find the pattern
  for (let answer = 0; answer < 10; answer++) {
    const input = `${serverChallenge}${prefix}${answer}`;
    const hash = sha3_512(input);
    console.log(`sha3_512(${input.slice(0, 40)}...${answer}) = ${hash.slice(0, 32)}...`);
  }
  
  // The WASM checks: 
  // 1. Writes challenge and prefix to WASM memory
  // 2. Calls wasm_solve which iterates answers
  // 3. For each answer, computes sha3_512(challenge + prefix + answer)
  // 4. Checks if the hash value meets the difficulty
  // 5. Returns the answer as float64
  
  // Let's figure out the difficulty check
  // Difficulty is 144000 — this might be a threshold on the hash interpreted as a number
  
  // The WASM returns status=1 and a float64 value
  // It reads 8 bytes from hash offset 8 (not 0!) and interprets as float64
  // Then checks: value / difficulty > some threshold
  
  // Let's brute-force a few to find when it returns
  console.log("\n=== Brute-forcing answer ===");
  for (let answer = 0; answer < 100_000; answer++) {
    const input = `${serverChallenge}${prefix}${answer}`;
    const hash = sha3_512(input);
    
    // Read bytes 8-15 as float64 (little-endian) — this matches the WASM
    const byteStr = hash.slice(16, 32); // bytes 8-15 as hex
    const bytes = new Uint8Array(8);
    for (let i = 0; i < 8; i++) {
      bytes[i] = parseInt(byteStr.slice(i * 2, i * 2 + 2), 16);
    }
    const view = new DataView(bytes.buffer);
    const value = view.getFloat64(0, true);
    
    // Check: value / difficulty > 0 && isFinite(value) && Math.abs(value) > 0
    if (isFinite(value) && value !== 0 && Math.abs(value) / serverDifficulty > 0) {
      console.log(`Found at answer=${answer}`);
      console.log(`Hash: ${hash}`);
      console.log(`Value (float64 from bytes 8-15): ${value}`);
      console.log(`value/difficulty: ${value / serverDifficulty}`);
      break;
    }
    
    if (answer % 10000 === 0) {
      // Show progress
    }
  }
}

verifyAlgorithm().catch(console.error);
