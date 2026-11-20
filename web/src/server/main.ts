// SPDX-License-Identifier: AGPL-3.0-or-later

import { mount } from 'svelte';
import '$shared/styles/base.css';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) {
	throw new Error('index.html has no #app element');
}
mount(App, { target });
