"use strict";
var __assign = (this && this.__assign) || function () {
    __assign = Object.assign || function(t) {
        for (var s, i = 1, n = arguments.length; i < n; i++) {
            s = arguments[i];
            for (var p in s) if (Object.prototype.hasOwnProperty.call(s, p))
                t[p] = s[p];
        }
        return t;
    };
    return __assign.apply(this, arguments);
};
var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
var __generator = (this && this.__generator) || function (thisArg, body) {
    var _ = { label: 0, sent: function() { if (t[0] & 1) throw t[1]; return t[1]; }, trys: [], ops: [] }, f, y, t, g = Object.create((typeof Iterator === "function" ? Iterator : Object).prototype);
    return g.next = verb(0), g["throw"] = verb(1), g["return"] = verb(2), typeof Symbol === "function" && (g[Symbol.iterator] = function() { return this; }), g;
    function verb(n) { return function (v) { return step([n, v]); }; }
    function step(op) {
        if (f) throw new TypeError("Generator is already executing.");
        while (g && (g = 0, op[0] && (_ = 0)), _) try {
            if (f = 1, y && (t = op[0] & 2 ? y["return"] : op[0] ? y["throw"] || ((t = y["return"]) && t.call(y), 0) : y.next) && !(t = t.call(y, op[1])).done) return t;
            if (y = 0, t) op = [op[0] & 2, t.value];
            switch (op[0]) {
                case 0: case 1: t = op; break;
                case 4: _.label++; return { value: op[1], done: false };
                case 5: _.label++; y = op[1]; op = [0]; continue;
                case 7: op = _.ops.pop(); _.trys.pop(); continue;
                default:
                    if (!(t = _.trys, t = t.length > 0 && t[t.length - 1]) && (op[0] === 6 || op[0] === 2)) { _ = 0; continue; }
                    if (op[0] === 3 && (!t || (op[1] > t[0] && op[1] < t[3]))) { _.label = op[1]; break; }
                    if (op[0] === 6 && _.label < t[1]) { _.label = t[1]; t = op; break; }
                    if (t && _.label < t[2]) { _.label = t[2]; _.ops.push(op); break; }
                    if (t[2]) _.ops.pop();
                    _.trys.pop(); continue;
            }
            op = body.call(thisArg, _);
        } catch (e) { op = [6, e]; y = 0; } finally { f = t = 0; }
        if (op[0] & 5) throw op[1]; return { value: op[0] ? op[1] : void 0, done: true };
    }
};
var __spreadArray = (this && this.__spreadArray) || function (to, from, pack) {
    if (pack || arguments.length === 2) for (var i = 0, l = from.length, ar; i < l; i++) {
        if (ar || !(i in from)) {
            if (!ar) ar = Array.prototype.slice.call(from, 0, i);
            ar[i] = from[i];
        }
    }
    return to.concat(ar || Array.prototype.slice.call(from));
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.renderSettingsModal = renderSettingsModal;
var api_js_1 = require("../api.js");
var utils_js_1 = require("../utils.js");
var theme_js_1 = require("../theme.js");
var selectors_js_1 = require("../state/selectors.js");
var mutations_js_1 = require("../state/mutations.js");
// Runtime access to view functions from app.js
// These will be available after app.js loads
function getLoadBoardBySlug() {
    return __awaiter(this, void 0, void 0, function () {
        var appModule, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    _b.trys.push([0, 2, , 3]);
                    return [4 /*yield*/, Promise.resolve().then(function () { return require('../../app.js'); })];
                case 1:
                    appModule = _b.sent();
                    return [2 /*return*/, appModule.loadBoardBySlug];
                case 2:
                    _a = _b.sent();
                    // Fallback to window if import fails
                    return [2 /*return*/, window.loadBoardBySlug || loadBoardBySlug];
                case 3: return [2 /*return*/];
            }
        });
    });
}
// Helper function for tag color
function getTagColor(tagName) {
    return (0, selectors_js_1.getTagColors)()[tagName] || null;
}
// Render burndown chart
function renderBurndownChart(data) {
    if (!data || data.length === 0) {
        return "<div class='muted'>No data available. Create some todos to see the burndown chart.</div>";
    }
    // Find max value for scaling
    var maxCount = Math.max.apply(Math, __spreadArray(__spreadArray([], data.map(function (p) { return p.incompleteCount; }), false), [1], false));
    var minCount = Math.min.apply(Math, __spreadArray(__spreadArray([], data.map(function (p) { return p.incompleteCount; }), false), [0], false));
    var range = maxCount - minCount || 1;
    // Calculate chart dimensions
    var chartHeight = 100;
    var paddingLeft = 8;
    var pointWidth = (100 - paddingLeft * 2) / data.length;
    // Generate points for the line (with padding)
    var points = data.map(function (point, index) {
        var x = paddingLeft + (index * pointWidth) + (pointWidth / 2);
        var y = chartHeight - ((point.incompleteCount - minCount) / range * chartHeight);
        return "".concat(x, ",").concat(y);
    }).join(" ");
    // Generate area path (line + bottom fill)
    var firstX = paddingLeft + (pointWidth / 2);
    var lastX = paddingLeft + ((data.length - 1) * pointWidth) + (pointWidth / 2);
    var areaPath = data.map(function (point, index) {
        var x = paddingLeft + (index * pointWidth) + (pointWidth / 2);
        var y = chartHeight - ((point.incompleteCount - minCount) / range * chartHeight);
        return index === 0 ? "M ".concat(x, " ").concat(chartHeight, " L ").concat(x, " ").concat(y) : "L ".concat(x, " ").concat(y);
    }).join(" ") + " L ".concat(lastX, " ").concat(chartHeight, " Z");
    // Format dates for x-axis
    var dateLabels = data.map(function (point) {
        var date = new Date(point.date);
        return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
    });
    return "\n    <div class=\"burndown-chart\">\n      <div class=\"burndown-chart__header\">\n        <div class=\"burndown-chart__title\">Burndown Chart (14 days)</div>\n        <div class=\"burndown-chart__current\">Current: ".concat(data[data.length - 1].incompleteCount, " incomplete</div>\n      </div>\n      <div class=\"burndown-chart__container\">\n        <svg class=\"burndown-chart__svg\" viewBox=\"0 0 100 ").concat(chartHeight + 20, "\">\n          <!-- Grid lines -->\n          ").concat([0, 0.25, 0.5, 0.75, 1].map(function (ratio) {
        var y = chartHeight - (ratio * chartHeight);
        var value = Math.round(minCount + (range * ratio));
        return "<line x1=\"".concat(paddingLeft, "\" y1=\"").concat(y, "\" x2=\"").concat(100 - paddingLeft, "\" y2=\"").concat(y, "\" class=\"burndown-chart__grid\" />\n                    <text x=\"2\" y=\"").concat(y + 2, "\" class=\"burndown-chart__grid-label\">").concat(value, "</text>");
    }).join(""), "\n          \n          <!-- Area fill -->\n          <path d=\"").concat(areaPath, "\" class=\"burndown-chart__area\" />\n          \n          <!-- Line -->\n          <polyline points=\"").concat(points, "\" class=\"burndown-chart__line\" fill=\"none\" />\n          \n          <!-- Data points -->\n          ").concat(data.map(function (point, index) {
        var x = paddingLeft + (index * pointWidth) + (pointWidth / 2);
        var y = chartHeight - ((point.incompleteCount - minCount) / range * chartHeight);
        return "<circle cx=\"".concat(x, "\" cy=\"").concat(y, "\" r=\"1.5\" class=\"burndown-chart__point\" />");
    }).join(""), "\n        </svg>\n        <div class=\"burndown-chart__xaxis\">\n          ").concat(dateLabels.map(function (label, index) {
        var x = paddingLeft + (index * pointWidth) + (pointWidth / 2);
        return "<span class=\"burndown-chart__xaxis-label\" style=\"left: ".concat(x, "%\">").concat(label, "</span>");
    }).join(""), "\n        </div>\n      </div>\n    </div>\n  ");
}
// Render backup tab HTML
function renderBackupTabHTML() {
    var isAnonymousMode = !(0, selectors_js_1.getAuthStatusAvailable)();
    var replaceDisabled = isAnonymousMode ? 'disabled' : '';
    var replaceHidden = isAnonymousMode ? 'style="display: none;"' : '';
    return "\n    <div class=\"settings-backup-section\">\n      <div class=\"settings-backup-export\">\n        <div class=\"settings-section__title\">Export Data</div>\n        <div class=\"settings-section__description muted\">Download all your projects, todos, and tags as a JSON file.</div>\n        <button class=\"btn\" type=\"button\" id=\"backupExportBtn\">Export Backup</button>\n      </div>\n      <div class=\"settings-backup-import\">\n        <div class=\"settings-section__title\">Import Data</div>\n        <div class=\"settings-section__description muted\">Restore from a backup file or merge data from another instance.</div>\n        <input type=\"file\" accept=\".json\" id=\"backupFileInput\" style=\"margin-bottom: 16px;\">\n        <div style=\"margin-bottom: 16px;\">\n          <label style=\"display: block; margin-bottom: 8px;\">\n            <input type=\"radio\" name=\"importMode\" value=\"merge\" checked>\n            <span>Merge & update (recommended)</span>\n          </label>\n          <label style=\"display: block; margin-bottom: 8px;\" ".concat(replaceHidden, ">\n            <input type=\"radio\" name=\"importMode\" value=\"replace\" ").concat(replaceDisabled, ">\n            <span>Replace all data</span>\n          </label>\n          <label style=\"display: block; margin-bottom: 8px;\">\n            <input type=\"radio\" name=\"importMode\" value=\"copy\">\n            <span>Create a copy</span>\n          </label>\n        </div>\n        <div id=\"backupPreview\" class=\"settings-backup-preview\" style=\"display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px;\"></div>\n        <div id=\"backupConfirmation\" style=\"display: none; margin-bottom: 16px;\">\n          <input type=\"text\" id=\"backupConfirmationInput\" placeholder=\"Type REPLACE to confirm\" class=\"settings-backup-confirmation\" style=\"width: 100%; padding: 8px;\">\n        </div>\n        <div id=\"backupWarnings\" class=\"settings-backup-warnings\" style=\"display: none; margin-bottom: 16px; padding: 12px; background: var(--panel); border-radius: 4px; color: var(--muted);\"></div>\n        <button class=\"btn\" type=\"button\" id=\"backupImportBtn\" disabled>Import</button>\n      </div>\n    </div>\n  ");
}
// Backup handlers
function handleBackupExport() {
    return __awaiter(this, void 0, void 0, function () {
        var response, err, blob, url, a, err_1;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    _b.trys.push([0, 5, , 6]);
                    return [4 /*yield*/, fetch("/api/backup/export", {
                            headers: {
                                "X-Scrumboy": "1"
                            }
                        })];
                case 1:
                    response = _b.sent();
                    if (!!response.ok) return [3 /*break*/, 3];
                    return [4 /*yield*/, response.json()];
                case 2:
                    err = _b.sent();
                    (0, utils_js_1.showToast)(((_a = err.error) === null || _a === void 0 ? void 0 : _a.message) || "Export failed");
                    return [2 /*return*/];
                case 3: return [4 /*yield*/, response.blob()];
                case 4:
                    blob = _b.sent();
                    url = window.URL.createObjectURL(blob);
                    a = document.createElement("a");
                    a.href = url;
                    a.download = "scrumboy-backup-".concat(new Date().toISOString().split('T')[0], ".json");
                    document.body.appendChild(a);
                    a.click();
                    document.body.removeChild(a);
                    window.URL.revokeObjectURL(url);
                    (0, utils_js_1.showToast)("Backup exported successfully");
                    return [3 /*break*/, 6];
                case 5:
                    err_1 = _b.sent();
                    (0, utils_js_1.showToast)(err_1.message || "Export failed");
                    return [3 /*break*/, 6];
                case 6: return [2 /*return*/];
            }
        });
    });
}
function handleBackupFileSelect(e) {
    return __awaiter(this, void 0, void 0, function () {
        var target, file, text, data, importMode, preview, previewEl, previewHTML, warningsEl, err_2;
        var _a, _b;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    target = e.target;
                    file = (_a = target.files) === null || _a === void 0 ? void 0 : _a[0];
                    if (!file) {
                        return [2 /*return*/];
                    }
                    _c.label = 1;
                case 1:
                    _c.trys.push([1, 4, , 5]);
                    return [4 /*yield*/, file.text()];
                case 2:
                    text = _c.sent();
                    data = JSON.parse(text);
                    // Validate structure
                    if (!data.version || !data.projects || !Array.isArray(data.projects)) {
                        (0, utils_js_1.showToast)("Invalid backup file format");
                        return [2 /*return*/];
                    }
                    // Store the data for import
                    (0, mutations_js_1.setBackupData)(data);
                    importMode = ((_b = document.querySelector('input[name="importMode"]:checked')) === null || _b === void 0 ? void 0 : _b.value) || "merge";
                    return [4 /*yield*/, (0, api_js_1.apiFetch)("/api/backup/preview", {
                            method: "POST",
                            body: JSON.stringify({
                                data: data,
                                importMode: importMode
                            })
                        })];
                case 3:
                    preview = _c.sent();
                    previewEl = document.getElementById("backupPreview");
                    if (previewEl) {
                        previewHTML = "<strong>Preview:</strong><br>";
                        previewHTML += "Projects: ".concat(preview.projects, "<br>");
                        previewHTML += "Todos: ".concat(preview.todos, "<br>");
                        previewHTML += "Tags: ".concat(preview.tags, "<br>");
                        if (preview.willDelete !== undefined) {
                            previewHTML += "Will delete: ".concat(preview.willDelete, " projects<br>");
                        }
                        if (preview.willUpdate !== undefined) {
                            previewHTML += "Will update: ".concat(preview.willUpdate, " items<br>");
                        }
                        if (preview.willCreate !== undefined) {
                            previewHTML += "Will create: ".concat(preview.willCreate, " items<br>");
                        }
                        previewEl.innerHTML = previewHTML;
                        previewEl.style.display = "block";
                    }
                    // Display warnings if any
                    if (preview.warnings && preview.warnings.length > 0) {
                        warningsEl = document.getElementById("backupWarnings");
                        if (warningsEl) {
                            warningsEl.innerHTML = "<strong>Warnings:</strong><br>".concat(preview.warnings.map(function (w) { return (0, utils_js_1.escapeHTML)(w); }).join("<br>"));
                            warningsEl.style.display = "block";
                        }
                    }
                    (0, mutations_js_1.setBackupPreview)(preview);
                    updateBackupUI();
                    return [3 /*break*/, 5];
                case 4:
                    err_2 = _c.sent();
                    (0, utils_js_1.showToast)(err_2.message || "Failed to read backup file");
                    (0, mutations_js_1.setBackupData)(null);
                    (0, mutations_js_1.setBackupPreview)(null);
                    updateBackupUI();
                    return [3 /*break*/, 5];
                case 5: return [2 /*return*/];
            }
        });
    });
}
function updateBackupUI() {
    var _a;
    // Use stored reference if available, otherwise find by ID
    var importBtn = ((0, selectors_js_1.getBackupImportBtn)() || document.getElementById("backupImportBtn"));
    var confirmationDiv = document.getElementById("backupConfirmation");
    var confirmationInput = document.getElementById("backupConfirmationInput");
    var importMode = ((_a = document.querySelector('input[name="importMode"]:checked')) === null || _a === void 0 ? void 0 : _a.value) || "merge";
    if (!(0, selectors_js_1.getBackupData)()) {
        if (importBtn) {
            importBtn.disabled = true;
        }
        if (confirmationDiv) {
            confirmationDiv.style.display = "none";
        }
        return;
    }
    // Show confirmation input for replace mode
    if (importMode === "replace") {
        if (confirmationDiv) {
            confirmationDiv.style.display = "block";
        }
        var isValid = confirmationInput && confirmationInput.value.trim() === "REPLACE";
        if (importBtn) {
            importBtn.disabled = !isValid;
            // Force update the disabled state
            if (isValid) {
                importBtn.removeAttribute("disabled");
            }
            else {
                importBtn.setAttribute("disabled", "disabled");
            }
        }
    }
    else {
        if (confirmationDiv) {
            confirmationDiv.style.display = "none";
        }
        if (importBtn) {
            importBtn.disabled = false;
            importBtn.removeAttribute("disabled");
        }
    }
}
function handleBackupImport() {
    return __awaiter(this, void 0, void 0, function () {
        var importBtn, importMode, confirmationInput, body, originalText, result, message, warningsEl, err_3, errorMsg, importBtn_1;
        var _a, _b, _c, _d, _e, _f;
        return __generator(this, function (_g) {
            switch (_g.label) {
                case 0:
                    console.log("handleBackupImport: Function called");
                    if (!(0, selectors_js_1.getBackupData)()) {
                        console.log("handleBackupImport: No backup data");
                        (0, utils_js_1.showToast)("No backup file selected");
                        return [2 /*return*/];
                    }
                    importBtn = ((0, selectors_js_1.getBackupImportBtn)() || document.getElementById("backupImportBtn"));
                    console.log("handleBackupImport: Button found", {
                        hasButton: !!importBtn,
                        isDisabled: importBtn === null || importBtn === void 0 ? void 0 : importBtn.disabled,
                        buttonText: importBtn === null || importBtn === void 0 ? void 0 : importBtn.textContent
                    });
                    if (importBtn && importBtn.disabled) {
                        console.log("handleBackupImport: Button is disabled, returning");
                        (0, utils_js_1.showToast)("Please complete the confirmation to enable import");
                        return [2 /*return*/];
                    }
                    importMode = ((_a = document.querySelector('input[name="importMode"]:checked')) === null || _a === void 0 ? void 0 : _a.value) || "merge";
                    confirmationInput = document.getElementById("backupConfirmationInput");
                    console.log("handleBackupImport: Mode and confirmation", {
                        importMode: importMode,
                        confirmationValue: confirmationInput === null || confirmationInput === void 0 ? void 0 : confirmationInput.value,
                        confirmationTrimmed: (_b = confirmationInput === null || confirmationInput === void 0 ? void 0 : confirmationInput.value) === null || _b === void 0 ? void 0 : _b.trim()
                    });
                    // Validate confirmation for replace mode
                    if (importMode === "replace") {
                        if (!confirmationInput || confirmationInput.value.trim() !== "REPLACE") {
                            console.log("handleBackupImport: Invalid confirmation");
                            (0, utils_js_1.showToast)("Please type REPLACE in the confirmation field");
                            return [2 /*return*/];
                        }
                    }
                    _g.label = 1;
                case 1:
                    _g.trys.push([1, 3, , 4]);
                    console.log("handleBackupImport: Starting", { importMode: importMode, hasData: !!(0, selectors_js_1.getBackupData)() });
                    body = {
                        data: (0, selectors_js_1.getBackupData)(),
                        importMode: importMode
                    };
                    if (importMode === "replace") {
                        body.confirmation = confirmationInput.value.trim();
                    }
                    console.log("handleBackupImport: Request body prepared", {
                        importMode: body.importMode,
                        hasData: !!body.data,
                        hasConfirmation: !!body.confirmation,
                        projectsCount: (_d = (_c = body.data) === null || _c === void 0 ? void 0 : _c.projects) === null || _d === void 0 ? void 0 : _d.length
                    });
                    // Show loading state
                    if (importBtn) {
                        importBtn.disabled = true;
                        importBtn.setAttribute("disabled", "disabled");
                        originalText = importBtn.textContent;
                        importBtn.textContent = "Importing...";
                    }
                    console.log("handleBackupImport: Calling API...");
                    return [4 /*yield*/, (0, api_js_1.apiFetch)("/api/backup/import", {
                            method: "POST",
                            body: JSON.stringify(body)
                        })];
                case 2:
                    result = _g.sent();
                    console.log("handleBackupImport: API call completed", result);
                    message = "Import completed: ";
                    if (result.imported !== undefined)
                        message += "".concat(result.imported, " projects imported, ");
                    if (result.updated !== undefined)
                        message += "".concat(result.updated, " updated, ");
                    if (result.created !== undefined)
                        message += "".concat(result.created, " created");
                    (0, utils_js_1.showToast)(message);
                    // Show warnings if any
                    if (result.warnings && result.warnings.length > 0) {
                        warningsEl = document.getElementById("backupWarnings");
                        if (warningsEl) {
                            warningsEl.innerHTML = "<strong>Warnings:</strong><br>".concat(result.warnings.map(function (w) { return (0, utils_js_1.escapeHTML)(w); }).join("<br>"));
                            warningsEl.style.display = "block";
                        }
                    }
                    // Reload the page to show updated data
                    setTimeout(function () {
                        window.location.reload();
                    }, 1500);
                    return [3 /*break*/, 4];
                case 3:
                    err_3 = _g.sent();
                    console.error("handleBackupImport: ERROR CAUGHT", err_3);
                    console.error("handleBackupImport: Error details", {
                        message: err_3.message,
                        status: err_3.status,
                        data: err_3.data,
                        stack: err_3.stack
                    });
                    errorMsg = err_3.message || ((_f = (_e = err_3.data) === null || _e === void 0 ? void 0 : _e.error) === null || _f === void 0 ? void 0 : _f.message) || "Import failed";
                    console.error("handleBackupImport: Showing toast with message:", errorMsg);
                    (0, utils_js_1.showToast)(errorMsg);
                    importBtn_1 = ((0, selectors_js_1.getBackupImportBtn)() || document.getElementById("backupImportBtn"));
                    if (importBtn_1) {
                        importBtn_1.disabled = false;
                        importBtn_1.removeAttribute("disabled");
                        importBtn_1.textContent = "Import";
                        console.log("handleBackupImport: Button restored");
                    }
                    else {
                        console.error("handleBackupImport: Could not find button to restore");
                    }
                    return [3 /*break*/, 4];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function setupBackupTab() {
    return __awaiter(this, void 0, void 0, function () {
        var exportBtn, fileInput, confirmationInput, importBtn, newImportBtn;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            exportBtn = document.getElementById("backupExportBtn");
            if (exportBtn) {
                exportBtn.addEventListener("click", handleBackupExport);
            }
            fileInput = document.getElementById("backupFileInput");
            if (fileInput) {
                fileInput.addEventListener("change", handleBackupFileSelect);
            }
            // Import mode radio buttons
            document.querySelectorAll('input[name="importMode"]').forEach(function (radio) {
                radio.addEventListener("change", function () {
                    // Clear confirmation input when switching modes
                    var confirmationInput = document.getElementById("backupConfirmationInput");
                    if (confirmationInput) {
                        confirmationInput.value = "";
                    }
                    // Update UI when mode changes
                    setTimeout(function () { return updateBackupUI(); }, 0);
                });
            });
            confirmationInput = document.getElementById("backupConfirmationInput");
            if (confirmationInput) {
                confirmationInput.addEventListener("input", function () {
                    // Update UI immediately when typing
                    updateBackupUI();
                });
                // Also trigger on paste
                confirmationInput.addEventListener("paste", function () {
                    setTimeout(function () { return updateBackupUI(); }, 0);
                });
                // Trigger on keyup as well to catch all changes
                confirmationInput.addEventListener("keyup", function () {
                    updateBackupUI();
                });
            }
            importBtn = document.getElementById("backupImportBtn");
            if (importBtn) {
                console.log("setupBackupTab: Found import button, setting up click handler");
                newImportBtn = importBtn.cloneNode(true);
                (_a = importBtn.parentNode) === null || _a === void 0 ? void 0 : _a.replaceChild(newImportBtn, importBtn);
                // Attach fresh listener
                newImportBtn.addEventListener("click", function (e) { return __awaiter(_this, void 0, void 0, function () {
                    var err_4;
                    return __generator(this, function (_a) {
                        switch (_a.label) {
                            case 0:
                                console.log("Import button clicked!");
                                e.preventDefault();
                                e.stopPropagation();
                                _a.label = 1;
                            case 1:
                                _a.trys.push([1, 3, , 4]);
                                return [4 /*yield*/, handleBackupImport()];
                            case 2:
                                _a.sent();
                                return [3 /*break*/, 4];
                            case 3:
                                err_4 = _a.sent();
                                console.error("Error in click handler:", err_4);
                                throw err_4;
                            case 4: return [2 /*return*/];
                        }
                    });
                }); });
                // Store reference for updateBackupUI
                (0, mutations_js_1.setBackupImportBtn)(newImportBtn);
                console.log("setupBackupTab: Click handler attached, button reference stored");
            }
            else {
                console.error("backupImportBtn not found when setting up backup tab");
            }
            // Call updateBackupUI to set initial state after a brief delay to ensure DOM is ready
            setTimeout(function () {
                updateBackupUI();
            }, 0);
            return [2 /*return*/];
        });
    });
}
// updateTagColor and deleteTag call view functions, so they need to import from app.js
// For now, we'll declare them and they'll be available at runtime from app.js
// TODO: These should be refactored to not call views directly
function updateTagColor(tagName, color) {
    return __awaiter(this, void 0, void 0, function () {
        var url, tagColors, clearBtn, loadBoard, err_5;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    url = null;
                    if ((0, selectors_js_1.getSlug)()) {
                        url = "/api/board/".concat((0, selectors_js_1.getSlug)(), "/tags/").concat(encodeURIComponent(tagName));
                    }
                    else if ((0, selectors_js_1.getSettingsProjectId)()) {
                        url = "/api/projects/".concat((0, selectors_js_1.getSettingsProjectId)(), "/tags/").concat(encodeURIComponent(tagName));
                    }
                    else {
                        (0, utils_js_1.showToast)("No project available");
                        return [2 /*return*/];
                    }
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 6, , 7]);
                    return [4 /*yield*/, (0, api_js_1.apiFetch)(url, {
                            method: "PATCH",
                            body: JSON.stringify({ color: color }),
                        })];
                case 2:
                    _a.sent();
                    tagColors = __assign({}, (0, selectors_js_1.getTagColors)());
                    if (color) {
                        tagColors[tagName] = color;
                    }
                    else {
                        delete tagColors[tagName];
                    }
                    (0, mutations_js_1.setTagColors)(tagColors);
                    clearBtn = document.querySelector(".settings-color-clear[data-tag=\"".concat((0, utils_js_1.escapeHTML)(tagName), "\"]"));
                    if (clearBtn) {
                        clearBtn.style.display = color ? "" : "none";
                    }
                    if (!(0, selectors_js_1.getSlug)()) return [3 /*break*/, 5];
                    return [4 /*yield*/, getLoadBoardBySlug()];
                case 3:
                    loadBoard = _a.sent();
                    return [4 /*yield*/, loadBoard((0, selectors_js_1.getSlug)(), (0, selectors_js_1.getTag)())];
                case 4:
                    _a.sent();
                    _a.label = 5;
                case 5:
                    (0, utils_js_1.showToast)("Tag color updated");
                    return [3 /*break*/, 7];
                case 6:
                    err_5 = _a.sent();
                    (0, utils_js_1.showToast)(err_5.message);
                    return [3 /*break*/, 7];
                case 7: return [2 /*return*/];
            }
        });
    });
}
function deleteTag(tagName) {
    return __awaiter(this, void 0, void 0, function () {
        var url, tagColors, loadBoard, err_6;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    url = null;
                    if ((0, selectors_js_1.getSlug)()) {
                        url = "/api/board/".concat((0, selectors_js_1.getSlug)(), "/tags/").concat(encodeURIComponent(tagName));
                    }
                    else if ((0, selectors_js_1.getSettingsProjectId)()) {
                        url = "/api/projects/".concat((0, selectors_js_1.getSettingsProjectId)(), "/tags/").concat(encodeURIComponent(tagName));
                    }
                    else {
                        (0, utils_js_1.showToast)("No project available");
                        return [2 /*return*/];
                    }
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 7, , 8]);
                    return [4 /*yield*/, (0, api_js_1.apiFetch)(url, {
                            method: "DELETE",
                        })];
                case 2:
                    _a.sent();
                    tagColors = __assign({}, (0, selectors_js_1.getTagColors)());
                    delete tagColors[tagName];
                    (0, mutations_js_1.setTagColors)(tagColors);
                    // Refresh settings modal to update the list - recursive call
                    return [4 /*yield*/, renderSettingsModal()];
                case 3:
                    // Refresh settings modal to update the list - recursive call
                    _a.sent();
                    if (!(0, selectors_js_1.getSlug)()) return [3 /*break*/, 6];
                    return [4 /*yield*/, getLoadBoardBySlug()];
                case 4:
                    loadBoard = _a.sent();
                    return [4 /*yield*/, loadBoard((0, selectors_js_1.getSlug)(), (0, selectors_js_1.getTag)())];
                case 5:
                    _a.sent();
                    _a.label = 6;
                case 6:
                    (0, utils_js_1.showToast)("Tag \"".concat(tagName, "\" deleted"));
                    return [3 /*break*/, 8];
                case 7:
                    err_6 = _a.sent();
                    (0, utils_js_1.showToast)(err_6.message);
                    return [3 /*break*/, 8];
                case 8: return [2 /*return*/];
            }
        });
    });
}
// Main render function
function renderSettingsModal() {
    return __awaiter(this, void 0, void 0, function () {
        var contentEl, showProfileTab, tagsURL, burndownURL, hasProjectAccess, projectId, durable, tags, tagsHTML, burndownData, tagColors_1, err_7, err_8, profileHTML, customizationHTML, tagColorsContent, chartsContent;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    contentEl = document.querySelector("#settingsDialog .dialog__content");
                    if (!contentEl) {
                        console.error("Settings dialog content element not found");
                        return [2 /*return*/];
                    }
                    showProfileTab = !!(0, selectors_js_1.getAuthStatusAvailable)();
                    tagsURL = null;
                    burndownURL = null;
                    hasProjectAccess = false;
                    if ((0, selectors_js_1.getSlug)()) {
                        tagsURL = "/api/board/".concat((0, selectors_js_1.getSlug)(), "/tags");
                        burndownURL = "/api/board/".concat((0, selectors_js_1.getSlug)(), "/burndown");
                        (0, mutations_js_1.setSettingsProjectId)(null);
                        hasProjectAccess = true;
                    }
                    else {
                        projectId = (0, selectors_js_1.getProjectId)() || (0, selectors_js_1.getSettingsProjectId)();
                        if (!projectId && Array.isArray((0, selectors_js_1.getProjects)()) && (0, selectors_js_1.getProjects)().length > 0) {
                            durable = (0, selectors_js_1.getProjects)().find(function (p) { return !p.expiresAt; });
                            projectId = (durable || (0, selectors_js_1.getProjects)()[0]).id;
                        }
                        if (projectId) {
                            (0, mutations_js_1.setSettingsProjectId)(projectId);
                            tagsURL = "/api/projects/".concat(projectId, "/tags");
                            burndownURL = "/api/projects/".concat(projectId, "/burndown");
                            hasProjectAccess = true;
                        }
                    }
                    // Initialize active tab (default to Profile or Customization if no projects)
                    if (!(0, selectors_js_1.getSettingsActiveTab)()) {
                        if (showProfileTab) {
                            (0, mutations_js_1.setSettingsActiveTab)("profile");
                        }
                        else if (hasProjectAccess) {
                            (0, mutations_js_1.setSettingsActiveTab)("tag-colors");
                        }
                        else {
                            (0, mutations_js_1.setSettingsActiveTab)("customization");
                        }
                    }
                    else if (!showProfileTab && (0, selectors_js_1.getSettingsActiveTab)() === "profile") {
                        (0, mutations_js_1.setSettingsActiveTab)(hasProjectAccess ? "tag-colors" : "customization");
                    }
                    tags = [];
                    tagsHTML = "";
                    burndownData = [];
                    if (!hasProjectAccess) return [3 /*break*/, 8];
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 7, , 8]);
                    return [4 /*yield*/, (0, api_js_1.apiFetch)(tagsURL)];
                case 2:
                    tags = _a.sent();
                    // Sort tags alphabetically by name
                    tags.sort(function (a, b) { return a.name.localeCompare(b.name); });
                    tagColors_1 = {};
                    tags.forEach(function (tag) {
                        if (tag.color) {
                            tagColors_1[tag.name] = tag.color;
                        }
                    });
                    (0, mutations_js_1.setTagColors)(tagColors_1);
                    tagsHTML = tags.length === 0
                        ? "<div class='muted'>No tags yet. Create todos with tags to see them here.</div>"
                        : tags.map(function (tag) {
                            var colorValue = tag.color || "#9CA3AF";
                            return "\n              <div class=\"settings-tag-item\">\n                <span class=\"settings-tag-name\" title=\"".concat((0, utils_js_1.escapeHTML)(tag.name), "\">").concat((0, utils_js_1.escapeHTML)(tag.name), "</span>\n                <div class=\"settings-tag-color-controls\">\n                  <input \n                    type=\"color\" \n                    class=\"settings-color-picker\" \n                    data-tag=\"").concat((0, utils_js_1.escapeHTML)(tag.name), "\"\n                    value=\"").concat(colorValue, "\"\n                    title=\"Tag color\"\n                  />\n                  <button \n                    class=\"btn btn--ghost btn--small settings-color-clear\" \n                    data-tag=\"").concat((0, utils_js_1.escapeHTML)(tag.name), "\"\n                    title=\"Clear color\"\n                    ").concat(!tag.color ? 'style="display: none;"' : '', "\n                  >Clear</button>\n                  <button \n                    class=\"btn btn--danger btn--small settings-tag-delete\" \n                    data-tag=\"").concat((0, utils_js_1.escapeHTML)(tag.name), "\"\n                    title=\"Delete tag\"\n                    aria-label=\"Delete tag\"\n                  >\u2715</button>\n                </div>\n              </div>\n            ");
                        }).join("");
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, api_js_1.apiFetch)(burndownURL)];
                case 4:
                    burndownData = _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    err_7 = _a.sent();
                    console.error("Failed to fetch burndown data:", err_7);
                    return [3 /*break*/, 6];
                case 6: return [3 /*break*/, 8];
                case 7:
                    err_8 = _a.sent();
                    console.error("Failed to fetch tags:", err_8);
                    tagsHTML = "<div class='muted'>Error loading tags: ".concat((0, utils_js_1.escapeHTML)(err_8.message), "</div>");
                    return [3 /*break*/, 8];
                case 8:
                    profileHTML = (function () {
                        if (!showProfileTab)
                            return "";
                        var u = (0, selectors_js_1.getUser)();
                        return "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Profile</div>\n        <div class=\"settings-section__description muted\">Signed-in user for this instance.</div>\n        ".concat(u ? "\n          <div class=\"settings-kv\">\n            <div class=\"settings-kv__row\"><div class=\"muted\">Name</div><div>".concat((0, utils_js_1.escapeHTML)(u.name || ""), "</div></div>\n            <div class=\"settings-kv__row\"><div class=\"muted\">Email</div><div>").concat((0, utils_js_1.escapeHTML)(u.email || ""), "</div></div>\n            <div class=\"settings-kv__row\"><div class=\"muted\">User ID</div><div>").concat(u.id != null ? (0, utils_js_1.escapeHTML)(String(u.id)) : "", "</div></div>\n            <div class=\"settings-kv__row\"><div class=\"muted\">Authentication</div><div>Authenticated</div></div>\n          </div>\n        ") : "\n          <div class=\"muted\">Not signed in.</div>\n        ", "\n      </div>\n    ");
                    })();
                    customizationHTML = "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Theme</div>\n        <div class=\"settings-section__description muted\">Choose your preferred color scheme.</div>\n        <div class=\"theme-selector\">\n          <label class=\"theme-option\">\n            <input type=\"radio\" name=\"theme\" value=\"system\" ".concat((0, theme_js_1.getStoredTheme)() === theme_js_1.THEME_SYSTEM ? 'checked' : '', ">\n            <div>\n              <div>System</div>\n              <div class=\"theme-option__description\">Follow your device's theme setting</div>\n            </div>\n          </label>\n          <label class=\"theme-option\">\n            <input type=\"radio\" name=\"theme\" value=\"dark\" ").concat((0, theme_js_1.getStoredTheme)() === theme_js_1.THEME_DARK ? 'checked' : '', ">\n            <div>\n              <div>Dark</div>\n              <div class=\"theme-option__description\">Always use dark mode</div>\n            </div>\n          </label>\n          <label class=\"theme-option\">\n            <input type=\"radio\" name=\"theme\" value=\"light\" ").concat((0, theme_js_1.getStoredTheme)() === theme_js_1.THEME_LIGHT ? 'checked' : '', ">\n            <div>\n              <div>Light</div>\n              <div class=\"theme-option__description\">Always use light mode</div>\n            </div>\n          </label>\n        </div>\n      </div>\n    ");
                    tagColorsContent = hasProjectAccess
                        ? "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Tag Colors</div>\n        <div class=\"settings-section__description muted\">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>\n        <div class=\"settings-tags-list\">\n          ".concat(tagsHTML, "\n        </div>\n      </div>\n    ")
                        : "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Tag Colors</div>\n        <div class=\"settings-section__description muted\">Assign custom colors to tags. Colors will appear in filter chips and todo cards.</div>\n        <div class=\"muted\">No projects available. Create a project to manage tag colors.</div>\n      </div>\n    ";
                    chartsContent = hasProjectAccess
                        ? "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Burndown Chart</div>\n        <div class=\"settings-section__description muted\">Track incomplete todos over the last 14 days.</div>\n        ".concat(renderBurndownChart(burndownData), "\n      </div>\n    ")
                        : "\n      <div class=\"settings-section\">\n        <div class=\"settings-section__title\">Burndown Chart</div>\n        <div class=\"settings-section__description muted\">Track incomplete todos over the last 14 days.</div>\n        <div class=\"muted\">No projects available. Create a project to view burndown charts.</div>\n      </div>\n    ";
                    contentEl.innerHTML = "\n    <div class=\"settings-tabs\">\n      ".concat(showProfileTab ? "<button class=\"settings-tab ".concat((0, selectors_js_1.getSettingsActiveTab)() === "profile" ? "settings-tab--active" : "", "\" data-tab=\"profile\">Profile</button>") : "", "\n      <button class=\"settings-tab ").concat((0, selectors_js_1.getSettingsActiveTab)() === "customization" ? "settings-tab--active" : "", "\" data-tab=\"customization\">Customization</button>\n      <button class=\"settings-tab ").concat((0, selectors_js_1.getSettingsActiveTab)() === "tag-colors" ? "settings-tab--active" : "", "\" data-tab=\"tag-colors\">Tag Colors</button>\n      <button class=\"settings-tab ").concat((0, selectors_js_1.getSettingsActiveTab)() === "charts" ? "settings-tab--active" : "", "\" data-tab=\"charts\">Charts</button>\n      <button class=\"settings-tab ").concat((0, selectors_js_1.getSettingsActiveTab)() === "backup" ? "settings-tab--active" : "", "\" data-tab=\"backup\">Backup</button>\n    </div>\n    <div class=\"settings-tab-content\" id=\"settingsTabContent\">\n      ").concat((0, selectors_js_1.getSettingsActiveTab)() === "profile" ? profileHTML : (0, selectors_js_1.getSettingsActiveTab)() === "customization" ? customizationHTML : (0, selectors_js_1.getSettingsActiveTab)() === "tag-colors" ? tagColorsContent : (0, selectors_js_1.getSettingsActiveTab)() === "charts" ? chartsContent : (0, selectors_js_1.getSettingsActiveTab)() === "backup" ? renderBackupTabHTML() : "", "\n    </div>\n  ");
                    // Setup tab switching
                    document.querySelectorAll(".settings-tab").forEach(function (tab) {
                        tab.addEventListener("click", function (e) { return __awaiter(_this, void 0, void 0, function () {
                            var tabName, dialog, currentHeight;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0:
                                        tabName = e.target.getAttribute("data-tab");
                                        if (!tabName) return [3 /*break*/, 2];
                                        (0, mutations_js_1.setSettingsActiveTab)(tabName);
                                        return [4 /*yield*/, renderSettingsModal()];
                                    case 1:
                                        _a.sent();
                                        dialog = document.getElementById("settingsDialog");
                                        if (dialog && dialog.open) {
                                            currentHeight = dialog.style.height;
                                            dialog.style.height = "auto";
                                            // Force a reflow
                                            void dialog.offsetHeight;
                                            // Reset to let CSS take over
                                            dialog.style.height = currentHeight || "";
                                        }
                                        _a.label = 2;
                                    case 2: return [2 /*return*/];
                                }
                            });
                        }); });
                    });
                    // Setup backup tab if it's active
                    if ((0, selectors_js_1.getSettingsActiveTab)() === "backup") {
                        // Wait a tick for DOM to be ready
                        setTimeout(function () {
                            setupBackupTab();
                        }, 0);
                    }
                    // Setup theme selector
                    document.querySelectorAll('input[name="theme"]').forEach(function (radio) {
                        radio.addEventListener('change', function (e) {
                            (0, theme_js_1.handleThemeChange)(e.target.value);
                        });
                    });
                    // Setup event listeners for color pickers (only if we have project access)
                    if (hasProjectAccess) {
                        document.querySelectorAll(".settings-color-picker").forEach(function (picker) {
                            picker.addEventListener("change", function (e) { return __awaiter(_this, void 0, void 0, function () {
                                var tagName, color;
                                return __generator(this, function (_a) {
                                    switch (_a.label) {
                                        case 0:
                                            tagName = e.target.getAttribute("data-tag");
                                            color = e.target.value;
                                            if (!tagName) return [3 /*break*/, 2];
                                            return [4 /*yield*/, updateTagColor(tagName, color)];
                                        case 1:
                                            _a.sent();
                                            _a.label = 2;
                                        case 2: return [2 /*return*/];
                                    }
                                });
                            }); });
                        });
                        // Setup event listeners for clear buttons
                        document.querySelectorAll(".settings-color-clear").forEach(function (btn) {
                            btn.addEventListener("click", function (e) { return __awaiter(_this, void 0, void 0, function () {
                                var tagName;
                                return __generator(this, function (_a) {
                                    switch (_a.label) {
                                        case 0:
                                            tagName = e.target.getAttribute("data-tag");
                                            if (!tagName) return [3 /*break*/, 2];
                                            return [4 /*yield*/, updateTagColor(tagName, null)];
                                        case 1:
                                            _a.sent();
                                            _a.label = 2;
                                        case 2: return [2 /*return*/];
                                    }
                                });
                            }); });
                        });
                        // Setup event listeners for delete buttons
                        document.querySelectorAll(".settings-tag-delete").forEach(function (btn) {
                            btn.addEventListener("click", function (e) { return __awaiter(_this, void 0, void 0, function () {
                                var tagName;
                                return __generator(this, function (_a) {
                                    switch (_a.label) {
                                        case 0:
                                            tagName = e.target.getAttribute("data-tag");
                                            if (!tagName) return [3 /*break*/, 2];
                                            if (!confirm("Delete tag \"".concat(tagName, "\" from all projects? This will remove it from all todos."))) {
                                                return [2 /*return*/];
                                            }
                                            return [4 /*yield*/, deleteTag(tagName)];
                                        case 1:
                                            _a.sent();
                                            _a.label = 2;
                                        case 2: return [2 /*return*/];
                                    }
                                });
                            }); });
                        });
                    }
                    return [2 /*return*/];
            }
        });
    });
}
