import { MilvusClient } from '@zilliz/milvus2-sdk-node';

const milvusAddr = "localhost:19530";
const collection = "messenger_message_chunks_v2";
const lmstudioUrl = "http://127.0.0.1:1234/v1";
const embedModel = "text-embedding-qwen3-embedding-8b";

async function getEmbedding(text) {
    const res = await fetch(`${lmstudioUrl}/embeddings`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: embedModel, input: text })
    });
    const data = await res.json();
    return data.data[0].embedding;
}

async function main() {
    const client = new MilvusClient({ address: milvusAddr });

    // Get collection stats
    const stats = await client.getCollectionStatistics({ collection_name: collection });
    console.log(`\nMilvus collection "${collection}":`);
    console.log(`  Total vectors: ${stats.data.row_count}`);

    // Search for "kot bob chonk"
    const query = "kot bob chonk";
    console.log(`\nSearching for: "${query}"`);

    const embedding = await getEmbedding(query);

    const results = await client.search({
        collection_name: collection,
        data: [embedding],
        anns_field: 'embedding',
        metric_type: 'COSINE',
        params: { ef: 200 },
        limit: 20,  // Get more results to see the full picture
        output_fields: ['chunk_id', 'thread_name', 'text', 'participant_names']
    });

    console.log(`\nTop 20 results (before quality filtering):`);
    console.log("=" .repeat(80));

    for (let i = 0; i < results.results.length; i++) {
        const r = results.results[i];
        const text = String(r.text || '').slice(0, 100).replace(/\n/g, ' ');
        const score = (r.score * 100).toFixed(1);
        const hasCat = /kot|cat|kotek/i.test(String(r.text || ''));
        const catMarker = hasCat ? ' 🐱' : '';
        console.log(`${(i+1).toString().padStart(2)}. [${score}%]${catMarker} ${r.thread_name}: "${text}..."`);
    }

    // Now specifically search for cat content
    console.log("\n\nSearching for Polish 'kot gruby' (fat cat):");
    const catQuery = "kot gruby";
    const catEmb = await getEmbedding(catQuery);

    const catResults = await client.search({
        collection_name: collection,
        data: [catEmb],
        anns_field: 'embedding',
        metric_type: 'COSINE',
        params: { ef: 200 },
        limit: 10,
        output_fields: ['chunk_id', 'thread_name', 'text']
    });

    console.log(`\nTop 10 results for "${catQuery}":`);
    for (let i = 0; i < catResults.results.length; i++) {
        const r = catResults.results[i];
        const text = String(r.text || '').slice(0, 100).replace(/\n/g, ' ');
        const score = (r.score * 100).toFixed(1);
        const hasCat = /kot|cat|kotek/i.test(String(r.text || ''));
        const catMarker = hasCat ? ' 🐱' : '';
        console.log(`${(i+1).toString().padStart(2)}. [${score}%]${catMarker} "${text}..."`);
    }

    await client.closeConnection();
}

main().catch(console.error);
