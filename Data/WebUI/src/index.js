////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/index.js

import React from 'react';
import ReactDOM from 'react-dom/client';

// Global Styles
import './index.css';
import "normalize.css/normalize.css";
import './Borealis.css'; // Global Theming for All of Borealis

import App from './App.jsx';

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);