<script lang="ts">
	import { T, Canvas } from '@threlte/core';
	import { OrbitControls, Grid, HTML } from '@threlte/extras';
	import * as THREE from 'three';
	import Scene from './Scene.svelte';

	interface Point {
		message_id: string;
		thread_id: string;
		sender_name: string;
		thread_name: string;
		text: string;
		timestamp_ms: number;
		score: number;
		x: number;
		y: number;
		z: number;
	}

	let {
		points,
		onPointSelect
	}: {
		points: Point[];
		onPointSelect?: (point: Point | null) => void;
	} = $props();
</script>

<Canvas>
	<Scene {points} {onPointSelect} />
</Canvas>
